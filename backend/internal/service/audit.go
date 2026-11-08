// Package service 业务逻辑层。
// audit.go：审计日志的写入（异步队列 + 批量落库）、查询、导出与保留期清理。
//
// 为什么不再「一条日志一个 goroutine」：报表查询是高频操作，每条一个 goroutine
// 会让协程数随日志量线性增长，且 SQLite 单写者下会增加锁竞争。这里改为
// 有界队列 + 单 worker 批量插入；队列满时丢弃日志并计数（业务响应优先）。
package service

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ce/report-system/backend/internal/model"
	"gorm.io/gorm"
)

const (
	auditBatchSize    = 200             // 攒够多少条立即落库
	auditFlushEvery   = 1 * time.Second // 或者最多等这么久
	auditCleanBatch   = 5000            // 清理时每批删除的行数（避免长事务锁库）
	auditCleanEvery   = 6 * time.Hour   // 自动清理间隔
	auditDefaultQueue = 2048            // 默认队列长度
)

// AuditConfig 审计日志写入配置
type AuditConfig struct {
	QueueSize int  // 队列长度；<=0 用默认 2048
	Sync      bool // true=同步写（排障用），false=异步队列
}

// AuditService 审计日志服务
type AuditService struct {
	db    *gorm.DB
	cfg   AuditConfig
	ch    chan AuditEntry
	drops int64 // 丢弃计数（atomic）

	started  atomic.Bool
	closed   atomic.Bool
	closeOne sync.Once
	wg       sync.WaitGroup
}

func NewAuditService(db *gorm.DB, cfg AuditConfig) *AuditService {
	size := cfg.QueueSize
	if size <= 0 {
		size = auditDefaultQueue
	}
	s := &AuditService{db: db, cfg: cfg}
	if !cfg.Sync {
		s.ch = make(chan AuditEntry, size)
	}
	return s
}

// AuditEntry 审计日志条目
type AuditEntry struct {
	LogType      string // login / query / export / config_change
	Module       string // report / datasource / dict / system / export
	UserID       int64
	Username     string
	ClientIP     string
	UserAgent    string
	TraceID      string
	Target       string // 目标名称快照（报表名、数据源名…）
	ReportID     *int64
	DatasourceID *int64
	Detail       string
	ErrMsg       string
	CostMs       int
	// Success 成功/失败；nil 视为成功。
	// 用指针而不是 bool：绝大多数埋点只记「成功动作」，漏写不该变成失败——
	// bool 的零值 false 会把所有忘记赋值的日志统统记成失败。
	Success *bool
}

// BoolPtr 构造 *bool，用于 AuditEntry.Success 这类「可选布尔」
func BoolPtr(v bool) *bool { return &v }

// ---------- 写入 ----------

// LogAsync 提交一条审计日志。队列满时丢弃并计数，不阻塞业务请求。
// 名称保留 LogAsync 以兼容既有调用方（内部已从「一条一协程」改为队列投递）。
func (s *AuditService) LogAsync(e AuditEntry) {
	if s.cfg.Sync || s.ch == nil || s.closed.Load() {
		s.write(e)
		return
	}
	// 关闭过程中（closed 已置位但 channel 刚关闭）仍可能命中「向已关闭 channel 发送」，
	// 这里兜底 recover 并退回同步写：审计日志不值得让整个进程崩掉。
	defer func() {
		if r := recover(); r != nil {
			s.write(e)
		}
	}()
	select {
	case s.ch <- e:
	default:
		// 每丢弃 1000 条提示一次，避免刷屏
		if n := atomic.AddInt64(&s.drops, 1); n%1000 == 1 {
			log.Printf("[audit] 队列已满，累计丢弃 %d 条审计日志（业务响应优先）", n)
		}
	}
}

// Dropped 已丢弃的日志条数（健康检查暴露，便于监控）
func (s *AuditService) Dropped() int64 { return atomic.LoadInt64(&s.drops) }

// Start 启动写入 worker（异步模式）。重复调用安全。
func (s *AuditService) Start() {
	if s.cfg.Sync || s.ch == nil {
		return
	}
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	s.wg.Add(1)
	go s.worker()
}

// Close 停止接收新日志并 flush 队列中剩余条目（服务退出时调用）
func (s *AuditService) Close() {
	s.closeOne.Do(func() {
		s.closed.Store(true)
		if s.ch != nil {
			close(s.ch)
		}
		s.wg.Wait()
	})
}

func (s *AuditService) worker() {
	defer s.wg.Done()
	ticker := time.NewTicker(auditFlushEvery)
	defer ticker.Stop()

	batch := make([]model.AuditLog, 0, auditBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.db.CreateInBatches(&batch, 100).Error; err != nil {
			log.Printf("[audit] 批量写入审计日志失败（%d 条）: %v", len(batch), err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case e, ok := <-s.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, toAuditModel(e))
			if len(batch) >= auditBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// write 同步写入单条（同步模式、队列未启动或已关闭时使用）
func (s *AuditService) write(e AuditEntry) {
	entry := toAuditModel(e)
	if err := s.db.Create(&entry).Error; err != nil {
		log.Printf("[audit] 写入审计日志失败: %v", err)
	}
}

// toAuditModel 把埋点条目转成待落库的记录（返回值，批量 append 用；需要指针时调用方自行取址）
func toAuditModel(e AuditEntry) model.AuditLog {
	// 只有显式标记失败才记 0；nil（未设置）= 成功
	success := 1
	if e.Success != nil && !*e.Success {
		success = 0
	}
	return model.AuditLog{
		LogType: e.LogType, Module: e.Module,
		UserID: e.UserID, Username: e.Username,
		ClientIP: e.ClientIP, UserAgent: e.UserAgent, TraceID: e.TraceID,
		Target:   e.Target,
		ReportID: e.ReportID, DatasourceID: e.DatasourceID,
		Detail: e.Detail, ErrMsg: e.ErrMsg, CostMs: e.CostMs,
		Success: success, // 显式赋值：该字段无库默认值，漏写会被记成 0（失败）
	}
}

// ---------- 查询 ----------

// AuditQuery 日志查询条件（零值表示不限制）
type AuditQuery struct {
	LogType      string
	Module       string
	Username     string
	Keyword      string // 匹配 detail / target / username
	Success      *bool  // nil=全部
	ReportID     int64
	DatasourceID int64
	From         *time.Time
	To           *time.Time
	Page         int
	PageSize     int
}

// Query 分页查询审计日志（按时间倒序）
func (s *AuditService) Query(q AuditQuery) ([]model.AuditLog, int64, error) {
	tx := s.applyQuery(s.db.Model(&model.AuditLog{}), q)

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page, size := q.Page, q.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	// 上限 1000：列表接口自身会限制到 200，导出按 1000 分块拉取，避免一次装下 10 万行
	if size > 1000 {
		size = 1000
	}

	var list []model.AuditLog
	err := s.applyQuery(s.db.Model(&model.AuditLog{}), q).
		Order("created_at DESC, id DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error
	return list, total, err
}

func (s *AuditService) applyQuery(tx *gorm.DB, q AuditQuery) *gorm.DB {
	if q.LogType != "" {
		tx = tx.Where("log_type = ?", q.LogType)
	}
	if q.Module != "" {
		tx = tx.Where("module = ?", q.Module)
	}
	if q.Username != "" {
		tx = tx.Where("username LIKE ?", "%"+q.Username+"%")
	}
	if q.Keyword != "" {
		like := "%" + q.Keyword + "%"
		// 用分组条件加括号：直接写 "a LIKE ? OR b LIKE ?" 会和其它 AND 条件抢优先级
		tx = tx.Where(s.db.Where("detail LIKE ?", like).Or("target LIKE ?", like).Or("username LIKE ?", like))
	}
	if q.Success != nil {
		v := 0
		if *q.Success {
			v = 1
		}
		tx = tx.Where("success = ?", v)
	}
	if q.ReportID > 0 {
		tx = tx.Where("report_id = ?", q.ReportID)
	}
	if q.DatasourceID > 0 {
		tx = tx.Where("datasource_id = ?", q.DatasourceID)
	}
	if q.From != nil {
		tx = tx.Where("created_at >= ?", *q.From)
	}
	if q.To != nil {
		tx = tx.Where("created_at <= ?", *q.To)
	}
	return tx
}

// AuditCount 分组统计项。SQL 别名用单字母 k/c：
// key 在 MySQL 里是保留字，直接 `AS key` 会语法报错。
type AuditCount struct {
	Key   string `gorm:"column:k" json:"key"`
	Count int64  `gorm:"column:c" json:"count"`
}

// AuditStats 日志概览（最近 N 天）
type AuditStats struct {
	Days    int          `json:"days"`
	Total   int64        `json:"total"`
	Failed  int64        `json:"failed"`
	ByType  []AuditCount `json:"byType"`
	TopUser []AuditCount `json:"topUser"`
}

// Stats 日志概览：总数、失败数、按类型分布、Top10 用户
func (s *AuditService) Stats(days int) (*AuditStats, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days)
	base := func() *gorm.DB {
		return s.db.Model(&model.AuditLog{}).Where("created_at >= ?", since)
	}

	out := &AuditStats{Days: days}
	if err := base().Count(&out.Total).Error; err != nil {
		return nil, err
	}
	if err := base().Where("success = 0").Count(&out.Failed).Error; err != nil {
		return nil, err
	}
	if err := base().Select("log_type AS k, COUNT(*) AS c").
		Group("log_type").Order("c DESC").Scan(&out.ByType).Error; err != nil {
		return nil, err
	}
	if err := base().Select("username AS k, COUNT(*) AS c").
		Where("username <> ''").Group("username").Order("c DESC").Limit(10).
		Scan(&out.TopUser).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- 清理 ----------

// DeleteBefore 删除 before 之前（不含）的日志，返回删除行数。
// 分批删除：SQLite/MySQL 下一次性删几十万行会长时间持锁，影响正在进行的查询写入。
func (s *AuditService) DeleteBefore(before time.Time) (int64, error) {
	var total int64
	for {
		var ids []int64
		if err := s.db.Model(&model.AuditLog{}).
			Where("created_at < ?", before).Order("id").Limit(auditCleanBatch).
			Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		res := s.db.Where("id IN ?", ids).Delete(&model.AuditLog{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if len(ids) < auditCleanBatch {
			return total, nil
		}
	}
}

// StartCleanup 按保留期自动清理（启动时先清一次，之后每 6 小时一次）。
// days <= 0 表示不清理（保留全部日志）。
func (s *AuditService) StartCleanup(days int) {
	if days <= 0 {
		log.Println("[audit] log.audit_retention_days <= 0，审计日志自动清理已关闭")
		return
	}
	go func() {
		for {
			cutoff := time.Now().AddDate(0, 0, -days)
			n, err := s.DeleteBefore(cutoff)
			switch {
			case err != nil:
				log.Printf("[audit] 清理审计日志失败: %v", err)
			case n > 0:
				log.Printf("[audit] 已清理 %d 条超过 %d 天的审计日志", n, days)
			}
			time.Sleep(auditCleanEvery)
		}
	}()
}

// ---------- 工具 ----------

// AuditLogTypes 合法日志类型（入参校验用）
var AuditLogTypes = map[string]bool{
	"login": true, "query": true, "export": true, "config_change": true,
}

// NormalizeLogType 归一小写并校验；不合法返回空串
func NormalizeLogType(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if AuditLogTypes[v] {
		return v
	}
	return ""
}
