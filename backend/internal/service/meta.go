// Package service 业务逻辑层。
// meta.go：元数据（表 / 字段 / 索引）扫描服务。
//
// 为什么要缓存：配置页的表名下拉是「输入一个字母查一次」，直连业务库扫描
// ALL_TABLES 在几千张表的 Oracle schema 下要 1s+，逐字输入等于逐字卡死。
// 这里按数据源整体缓存表清单，关键字改成内存过滤，远程搜索不再产生 IO。
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ce/report-system/backend/internal/datasource"
	"gorm.io/gorm"
)

// 单数据源表清单的硬上限：防止超大 schema（几万张表）把内存和响应撑爆。
// 触顶时只保留前 N 张并打日志，正常使用应配合关键字搜索。
const metaMaxTables = 50000

// 元数据扫描超时。扫描挂在第一个请求的 goroutine 上，若它中途断开（关页面/刷新），
// 用 context.WithoutCancel 派生独立上下文，避免连带把等待同一个 key 的其他请求一起失败。
const metaScanTimeout = 60 * time.Second

// MetaService 元数据扫描服务（表 / 字段清单，供 SQL 编写与条件配置使用）
type MetaService struct {
	ds    *DatasourceService
	ttl   time.Duration // 缓存有效期；<=0 表示关闭缓存（每次实时扫描）
	limit int           // 单次返回表数量上限；<=0 表示不限制

	mu       sync.Mutex
	tables   map[int64]*tableEntry    // key: 数据源 ID
	cols     map[string]*colEntry     // key: "数据源ID|小写表名"
	inflight map[string]*inflightCall // 单飞：同一 key 并发请求只扫描一次
	// gen 每个数据源的「失效代数」：Invalidate 时 +1。
	// 扫描开始时取快照，写回缓存前比对——期间发生过失效就丢弃这次结果，
	// 否则「改数据源」与「正在跑的旧扫描」撞车时，旧数据会被写回缓存，
	// 表现得像失效没生效（最长 TTL 内一直看到旧表）。
	gen map[int64]uint64
}

type tableEntry struct {
	items    []datasource.TableMeta
	loadedAt time.Time
}

type colEntry struct {
	items    []datasource.ColumnMeta
	loadedAt time.Time
}

// inflightCall 正在进行的扫描；done 关闭即表示扫描结束（成功或失败）
type inflightCall struct {
	done chan struct{}
}

// TableListResult 表清单响应：除数据外回传总数/截断/缓存命中信息，便于前端提示。
// 关键字过滤在缓存的全量数据上做，因此 total 是「匹配到的数量」，不是数据源的总表数。
type TableListResult struct {
	Items     []datasource.TableMeta `json:"items"`
	Total     int                    `json:"total"`     // 关键字匹配到的条数
	Truncated bool                   `json:"truncated"` // 是否因 table_limit 被截断
	Cached    bool                   `json:"cached"`    // 是否命中缓存
	AgeSec    int                    `json:"ageSec"`    // 缓存年龄（秒），0 表示刚扫描
}

// ColumnListResult 字段清单响应
type ColumnListResult struct {
	Items  []datasource.ColumnMeta `json:"items"`
	Cached bool                    `json:"cached"`
	AgeSec int                     `json:"ageSec"`
}

func NewMetaService(ds *DatasourceService, ttlSec, limit int) *MetaService {
	return &MetaService{
		ds:       ds,
		ttl:      time.Duration(ttlSec) * time.Second,
		limit:    limit,
		tables:   map[int64]*tableEntry{},
		cols:     map[string]*colEntry{},
		inflight: map[string]*inflightCall{},
		gen:      map[int64]uint64{},
	}
}

// Invalidate 清空指定数据源的元数据缓存（数据源配置变更 / 删除 / 连接测试后调用）。
// 表与字段一起清：表清单变了字段清单必然也要重扫，分开维护只会漏。
func (s *MetaService) Invalidate(datasourceID int64) {
	prefix := strconv.FormatInt(datasourceID, 10) + "|"
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tables, datasourceID)
	for k := range s.cols {
		if strings.HasPrefix(k, prefix) {
			delete(s.cols, k)
		}
	}
	s.gen[datasourceID]++ // 让在途扫描的结果作废（见 loadTables/loadColumns 的写回校验）
}

// Tables 表（含视图）清单；keyword 在缓存的全量清单上做内存过滤（大小写不敏感）。
// refresh=true 时跳过缓存强制重扫（前端「刷新表清单」按钮）。
// limit>0 时覆盖配置里的默认上限（前端适配小屏/内嵌场景时可少取一些）。
func (s *MetaService) Tables(ctx context.Context, datasourceID int64, keyword string, refresh bool, limit int) (*TableListResult, error) {
	entry, cached, err := s.loadTables(ctx, datasourceID, refresh)
	if err != nil {
		return nil, err
	}

	kw := strings.ToUpper(strings.TrimSpace(keyword))
	matched := entry.items
	if kw != "" {
		matched = make([]datasource.TableMeta, 0, len(entry.items))
		for _, t := range entry.items {
			if strings.Contains(strings.ToUpper(t.Name), kw) {
				matched = append(matched, t)
			}
		}
	}

	res := &TableListResult{
		Total:  len(matched),
		Cached: cached,
		AgeSec: ageSecOf(entry.loadedAt),
	}
	maxN := s.limit
	if limit > 0 {
		maxN = limit
	}
	items := matched
	if maxN > 0 && len(items) > maxN {
		items = items[:maxN]
		res.Truncated = true
	}
	// 返回的是缓存切片的只读视图（不拷贝，几千条拷贝一次不值得）：调用方不得原地改写
	res.Items = items
	return res, nil
}

// Columns 指定表的字段清单
func (s *MetaService) Columns(ctx context.Context, datasourceID int64, table string, refresh bool) (*ColumnListResult, error) {
	table = strings.TrimSpace(table)
	if table == "" {
		return nil, errors.New("表名不能为空")
	}
	items, loadedAt, cached, err := s.loadColumns(ctx, datasourceID, table, refresh)
	if err != nil {
		return nil, err
	}
	return &ColumnListResult{Items: items, Cached: cached, AgeSec: ageSecOf(loadedAt)}, nil
}

// Indexes 指定表的索引清单（驱动未实现时返回「不支持」错误，由 handler 转成可读提示）
func (s *MetaService) Indexes(ctx context.Context, datasourceID int64, table string) ([]datasource.IndexMeta, error) {
	table = strings.TrimSpace(table)
	if table == "" {
		return nil, errors.New("表名不能为空")
	}
	db, _, err := s.ds.GetPool(ctx, datasourceID)
	if err != nil {
		return nil, err
	}
	driver, err := s.driverOf(datasourceID)
	if err != nil {
		return nil, err
	}
	list, err := datasource.IndexesFor(ctx, driver, db, table)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []datasource.IndexMeta{}
	}
	return list, nil
}

// ---------- 缓存装载 ----------

func (s *MetaService) loadTables(ctx context.Context, datasourceID int64, refresh bool) (*tableEntry, bool, error) {
	if s.ttl <= 0 { // 缓存关闭：直接扫描
		e, err := s.scanTables(ctx, datasourceID)
		return e, false, err
	}

	s.mu.Lock()
	if !refresh {
		if e, ok := s.tables[datasourceID]; ok && time.Since(e.loadedAt) < s.ttl {
			s.mu.Unlock()
			return e, true, nil
		}
	}
	key := "t:" + strconv.FormatInt(datasourceID, 10)
	if f, ok := s.inflight[key]; ok { // 已有扫描在进行：等它结束直接读缓存
		s.mu.Unlock()
		if err := waitInflight(ctx, f); err != nil {
			return nil, false, err
		}
		s.mu.Lock()
		e := s.tables[datasourceID]
		s.mu.Unlock()
		if e != nil {
			return e, true, nil
		}
		// 先到者扫描失败（没写缓存）：自己再扫一次，避免把失败结果传染给后续请求
		e, err := s.scanTables(ctx, datasourceID)
		return e, false, err
	}
	f := &inflightCall{done: make(chan struct{})}
	s.inflight[key] = f
	genAt := s.gen[datasourceID] // 记下扫描开始时的代数
	s.mu.Unlock()

	e, err := s.scanTables(ctx, datasourceID)

	s.mu.Lock()
	delete(s.inflight, key)
	// 空结果不写缓存：一次失败的扫描不应该让用户 5 分钟内都看不到表；
	// 代数变了说明扫描期间数据源被改/删过，这份结果已不可信，同样不写回
	if err == nil && len(e.items) > 0 && s.gen[datasourceID] == genAt {
		s.tables[datasourceID] = e
	}
	s.mu.Unlock()
	close(f.done)
	return e, false, err
}

func (s *MetaService) loadColumns(ctx context.Context, datasourceID int64, table string, refresh bool) ([]datasource.ColumnMeta, time.Time, bool, error) {
	if s.ttl <= 0 {
		items, err := s.scanColumns(ctx, datasourceID, table)
		return items, time.Now(), false, err
	}

	key := colKey(datasourceID, table)
	s.mu.Lock()
	if !refresh {
		if e, ok := s.cols[key]; ok && time.Since(e.loadedAt) < s.ttl {
			items, loadedAt := e.items, e.loadedAt
			s.mu.Unlock()
			return items, loadedAt, true, nil
		}
	}
	if f, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		if err := waitInflight(ctx, f); err != nil {
			return nil, time.Time{}, false, err
		}
		s.mu.Lock()
		e := s.cols[key]
		s.mu.Unlock()
		if e != nil {
			return e.items, e.loadedAt, true, nil
		}
		items, err := s.scanColumns(ctx, datasourceID, table)
		return items, time.Now(), false, err
	}
	f := &inflightCall{done: make(chan struct{})}
	s.inflight[key] = f
	genAt := s.gen[datasourceID]
	s.mu.Unlock()

	items, err := s.scanColumns(ctx, datasourceID, table)
	now := time.Now()

	s.mu.Lock()
	delete(s.inflight, key)
	if err == nil && len(items) > 0 && s.gen[datasourceID] == genAt {
		s.cols[key] = &colEntry{items: items, loadedAt: now}
	}
	s.mu.Unlock()
	close(f.done)
	return items, now, false, err
}

func (s *MetaService) scanTables(ctx context.Context, datasourceID int64) (*tableEntry, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metaScanTimeout)
	defer cancel()
	db, _, err := s.ds.GetPool(ctx, datasourceID)
	if err != nil {
		return nil, err
	}
	driver, err := s.driverOf(datasourceID)
	if err != nil {
		return nil, err
	}
	// 一次取全量（keyword 传空），之后都在内存里过滤
	list, err := driver.ListTables(ctx, db, "")
	if err != nil {
		return nil, fmt.Errorf("表清单扫描失败: %w", err)
	}
	if len(list) > metaMaxTables {
		list = list[:metaMaxTables]
	}
	return &tableEntry{items: list, loadedAt: time.Now()}, nil
}

func (s *MetaService) scanColumns(ctx context.Context, datasourceID int64, table string) ([]datasource.ColumnMeta, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metaScanTimeout)
	defer cancel()
	db, _, err := s.ds.GetPool(ctx, datasourceID)
	if err != nil {
		return nil, err
	}
	driver, err := s.driverOf(datasourceID)
	if err != nil {
		return nil, err
	}
	list, err := driver.ListColumns(ctx, db, table)
	if err != nil {
		return nil, fmt.Errorf("字段扫描失败: %w", err)
	}
	return list, nil
}

// DriverOf 返回数据源对应的驱动实现（供标识符转义等场景复用）
func (s *MetaService) DriverOf(datasourceID int64) (datasource.Driver, error) {
	return s.driverOf(datasourceID)
}

func (s *MetaService) driverOf(datasourceID int64) (datasource.Driver, error) {
	ds, err := s.ds.Get(datasourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("数据源不存在")
		}
		return nil, err
	}
	return datasource.Get(ds.DbType)
}

// ---------- 工具 ----------

// colKey 字段缓存 key：库内表名大小写不敏感（Oracle 全大写、MySQL 视平台而定），统一小写
func colKey(datasourceID int64, table string) string {
	return strconv.FormatInt(datasourceID, 10) + "|" + strings.ToLower(strings.TrimSpace(table))
}

func ageSecOf(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	n := int(time.Since(t).Seconds())
	if n < 0 {
		return 0
	}
	return n
}

// waitInflight 等待正在进行的扫描，同时响应请求取消
func waitInflight(ctx context.Context, f *inflightCall) error {
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
