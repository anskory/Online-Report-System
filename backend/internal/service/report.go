package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ce/report-system/backend/internal/model"
	"gorm.io/gorm"
)

// 报表业务错误
var (
	ErrReportNotFound  = errors.New("报表不存在")
	ErrSQLRequired     = errors.New("SQL 不能为空")
	ErrParamNotInSQL   = errors.New("条件参数未出现在 SQL 中")
	ErrDSNNotFound     = errors.New("数据源不存在")
)

// reportConfigCache 报表配置缓存（60s TTL），避免每次查询都查三张表
type reportConfigCache struct {
	mu    sync.RWMutex
	items map[int64]*cachedReport
}

type cachedReport struct {
	report   *model.Report
	params   []model.ReportParam
	columns  []model.ReportColumn
	loadedAt time.Time
}

var reportCache = &reportConfigCache{items: map[int64]*cachedReport{}}

func cacheGet(id int64) (*cachedReport, bool) {
	reportCache.mu.RLock()
	defer reportCache.mu.RUnlock()
	c, ok := reportCache.items[id]
	if !ok || time.Since(c.loadedAt) > 60*time.Second {
		return nil, false
	}
	return c, true
}

func cachePut(r *model.Report, p []model.ReportParam, c []model.ReportColumn) {
	reportCache.mu.Lock()
	defer reportCache.mu.Unlock()
	reportCache.items[r.ID] = &cachedReport{report: r, params: p, columns: c, loadedAt: time.Now()}
}

func cacheInvalidate(id int64) {
	reportCache.mu.Lock()
	defer reportCache.mu.Unlock()
	delete(reportCache.items, id)
}

// ReportService 报表配置服务
type ReportService struct {
	db    *gorm.DB
	audit *AuditService
}

func NewReportService(db *gorm.DB, audit *AuditService) *ReportService {
	return &ReportService{db: db, audit: audit}
}

// ReportInput 报表保存入参
type ReportInput struct {
	Name         string               `json:"name" binding:"required"`
	GroupID      int64                `json:"groupId" binding:"required"`
	DatasourceID int64                `json:"datasourceId" binding:"required"`
	SQLText      string               `json:"sqlText" binding:"required"`
	QueryTimeout int                  `json:"queryTimeout"`
	RowLimit     int                  `json:"rowLimit"`
	ExportLimit  int64                `json:"exportLimit"`
	DefaultOrder string               `json:"defaultOrder"`
	Remark       string               `json:"remark"`
	Params       []model.ReportParam  `json:"params"`
	Columns      []model.ReportColumn `json:"columns"`
	RoleCodes    []string             `json:"roleCodes"`
}

// List 报表列表（admin 全量；普通用户仅已发布且任一角色被授权的）
func (s *ReportService) List(roles []string) ([]model.Report, error) {
	var list []model.Report
	q := s.db.Model(&model.Report{})
	if !IsAdmin(roles) {
		if len(roles) == 0 {
			return list, nil
		}
		q = q.Where("status = 'published' AND id IN (?)",
			s.db.Model(&model.RoleReport{}).Select("report_id").Where("role_code IN ?", roles))
	}
	err := q.Order("id DESC").Find(&list).Error
	return list, err
}

// ListGroups 分组列表
func (s *ReportService) ListGroups() ([]model.ReportGroup, error) {
	var list []model.ReportGroup
	err := s.db.Order("sort_no, id").Find(&list).Error
	return list, err
}

// CreateGroup 新建分组
func (s *ReportService) CreateGroup(name string) (*model.ReportGroup, error) {
	g := &model.ReportGroup{Name: name}
	if err := s.db.Create(g).Error; err != nil {
		return nil, err
	}
	return g, nil
}

// GetFull 加载报表完整配置（带缓存）
func (s *ReportService) GetFull(id int64) (*model.Report, []model.ReportParam, []model.ReportColumn, error) {
	if c, ok := cacheGet(id); ok {
		return c.report, c.params, c.columns, nil
	}
	var r model.Report
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, ErrReportNotFound
		}
		return nil, nil, nil, err
	}
	var params []model.ReportParam
	if err := s.db.Where("report_id = ?", id).Order("sort_no, id").Find(&params).Error; err != nil {
		return nil, nil, nil, err
	}
	var cols []model.ReportColumn
	if err := s.db.Where("report_id = ?", id).Order("sort_no, id").Find(&cols).Error; err != nil {
		return nil, nil, nil, err
	}
	cachePut(&r, params, cols)
	return &r, params, cols, nil
}

// Save 新建或更新报表（含条件、列、角色授权），返回报表 ID
func (s *ReportService) Save(id int64, in *ReportInput, userID int64, username string) (int64, error) {
	if err := validateSQLText(in.SQLText); err != nil {
		return 0, err
	}
	// 参数名与 SQL 中占位符的一致性校验（除 in/range 展开形式外）
	if err := validateParamsInSQL(in.SQLText, in.Params); err != nil {
		return 0, err
	}
	// 默认值
	if in.QueryTimeout <= 0 {
		in.QueryTimeout = 30
	}
	if in.RowLimit <= 0 {
		in.RowLimit = 100000
	}
	if in.ExportLimit <= 0 {
		in.ExportLimit = 5000000
	}

	var savedID int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var r model.Report
		if id > 0 {
			if err := tx.First(&r, id).Error; err != nil {
				return ErrReportNotFound
			}
		}
		r.Name, r.GroupID, r.DatasourceID, r.SQLText = in.Name, in.GroupID, in.DatasourceID, in.SQLText
		r.QueryTimeout, r.RowLimit, r.ExportLimit = in.QueryTimeout, in.RowLimit, in.ExportLimit
		r.DefaultOrder, r.Remark = in.DefaultOrder, in.Remark
		if id == 0 {
			// 仅新建时记录创建人；更新不得覆盖原作者
			r.CreatedBy = userID
		}

		var err error
		if id > 0 {
			err = tx.Save(&r).Error
		} else {
			err = tx.Create(&r).Error
		}
		if err != nil {
			return err
		}
		// 条件/列/角色全量替换
		if err := tx.Where("report_id = ?", r.ID).Delete(&model.ReportParam{}).Error; err != nil {
			return err
		}
		if err := tx.Where("report_id = ?", r.ID).Delete(&model.ReportColumn{}).Error; err != nil {
			return err
		}
		if err := tx.Where("report_id = ?", r.ID).Delete(&model.RoleReport{}).Error; err != nil {
			return err
		}
		for i := range in.Params {
			in.Params[i].ID = 0
			in.Params[i].ReportID = r.ID
		}
		for i := range in.Columns {
			in.Columns[i].ID = 0
			in.Columns[i].ReportID = r.ID
		}
		if len(in.Params) > 0 {
			if err := tx.Create(&in.Params).Error; err != nil {
				return err
			}
		}
		if len(in.Columns) > 0 {
			if err := tx.Create(&in.Columns).Error; err != nil {
				return err
			}
		}
		for _, rc := range in.RoleCodes {
			if err := tx.Create(&model.RoleReport{RoleCode: rc, ReportID: r.ID}).Error; err != nil {
				return err
			}
		}
		savedID = r.ID
		return nil
	})
	if err != nil {
		return 0, err
	}
	if savedID > 0 {
		id = savedID
	}
	cacheInvalidate(id)
	s.audit.LogAsync(AuditEntry{LogType: "config_change", UserID: userID, Username: username, ReportID: &id, Detail: fmt.Sprintf("保存报表 %s", in.Name)})
	return id, nil
}

// Delete 删除报表
func (s *ReportService) Delete(id int64, userID int64, username string) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Delete(&model.Report{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrReportNotFound
		}
		if err := tx.Where("report_id = ?", id).Delete(&model.ReportParam{}).Error; err != nil {
			return err
		}
		if err := tx.Where("report_id = ?", id).Delete(&model.ReportColumn{}).Error; err != nil {
			return err
		}
		if err := tx.Where("report_id = ?", id).Delete(&model.RoleReport{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	cacheInvalidate(id)
	s.audit.LogAsync(AuditEntry{LogType: "config_change", UserID: userID, Username: username, ReportID: &id, Detail: "删除报表"})
	return nil
}

// SetStatus 发布/停用（draft → published → disabled）
func (s *ReportService) SetStatus(id int64, status string, userID int64, username string) error {
	if status != "published" && status != "disabled" && status != "draft" {
		return errors.New("非法状态")
	}
	if err := s.db.Model(&model.Report{}).Where("id = ?", id).Update("status", status).Error; err != nil {
		return err
	}
	cacheInvalidate(id)
	s.audit.LogAsync(AuditEntry{LogType: "config_change", UserID: userID, Username: username, ReportID: &id, Detail: "报表状态 → " + status})
	return nil
}

// EnablePublic 开启公开访问，返回明文令牌（仅此一次可见）
func (s *ReportService) EnablePublic(id int64, userID int64, username string) (publicID, token string, err error) {
	var r model.Report
	if err = s.db.First(&r, id).Error; err != nil {
		return "", "", err
	}
	pid := r.PublicID
	if pid == nil || *pid == "" {
		pid = newString(randHex(6)) // 12 个 hex 字符
	}
	token = randHex(16) // 32 个 hex 字符
	sum := sha256.Sum256([]byte(token))
	err = s.db.Model(&model.Report{}).Where("id = ?", id).Updates(map[string]any{
		"public_id": *pid, "public_token": hex.EncodeToString(sum[:]), "is_public": 1,
	}).Error
	if err != nil {
		return "", "", err
	}
	cacheInvalidate(id)
	s.audit.LogAsync(AuditEntry{LogType: "config_change", UserID: userID, Username: username, ReportID: &id, Detail: "开启公开访问"})
	return *pid, token, nil
}

// DisablePublic 关闭公开访问
func (s *ReportService) DisablePublic(id int64, userID int64, username string) error {
	err := s.db.Model(&model.Report{}).Where("id = ?", id).Updates(map[string]any{"is_public": 0, "public_token": ""}).Error
	cacheInvalidate(id)
	if err == nil {
		s.audit.LogAsync(AuditEntry{LogType: "config_change", UserID: userID, Username: username, ReportID: &id, Detail: "关闭公开访问"})
	}
	return err
}

// SetPublicExport 设置报表「允许公开导出」开关（1=允许 0=禁止）。
// 公开访问页的导出按钮与导出接口均受此开关控制
func (s *ReportService) SetPublicExport(id int64, allow int, userID int64, username string) error {
	err := s.db.Model(&model.Report{}).Where("id = ?", id).
		Update("pub_allow_export", allow).Error
	cacheInvalidate(id)
	if err == nil {
		detail := "禁止公开导出"
		if allow == 1 {
			detail = "允许公开导出"
		}
		s.audit.LogAsync(AuditEntry{LogType: "config_change", UserID: userID, Username: username, ReportID: &id, Detail: detail})
	}
	return err
}

// IsAuthorized 任一角色是否可查询指定报表（需已发布且授权）
func (s *ReportService) IsAuthorized(roles []string, reportID int64) bool {
	if IsAdmin(roles) {
		return true
	}
	if len(roles) == 0 {
		return false
	}
	var r model.Report
	if err := s.db.First(&r, reportID).Error; err != nil || r.Status != "published" {
		return false
	}
	var cnt int64
	s.db.Model(&model.RoleReport{}).
		Where("role_code IN ? AND report_id = ?", roles, reportID).Count(&cnt)
	return cnt > 0
}

// GetDetail 报表详情（含条件与列配置）。普通用户要求已发布且授权，且隐藏 SQL 与数据源信息
func (s *ReportService) GetDetail(id int64, roles []string) (*model.Report, []model.ReportParam, []model.ReportColumn, error) {
	r, params, cols, err := s.GetFull(id)
	if err != nil {
		return nil, nil, nil, err
	}
	if !IsAdmin(roles) {
		if r.Status != "published" || !s.IsAuthorized(roles, id) {
			return nil, nil, nil, ErrReportNotFound
		}
		cp := *r
		cp.SQLText = ""
		cp.PublicToken = ""
		r = &cp
	}
	return r, params, cols, nil
}

// RoleCodesOf 已授权给该报表的角色列表（配置页回显用）
func (s *ReportService) RoleCodesOf(reportID int64) []string {
	var codes []string
	s.db.Model(&model.RoleReport{}).Where("report_id = ?", reportID).
		Order("role_code").Pluck("role_code", &codes)
	return codes
}

// CheckPublicToken 校验公开访问令牌
func (s *ReportService) CheckPublicToken(publicID, token string) (*model.Report, bool) {
	var r model.Report
	if err := s.db.Where("public_id = ? AND is_public = 1 AND status = 'published'", publicID).First(&r).Error; err != nil {
		return nil, false
	}
	sum := sha256.Sum256([]byte(token))
	if r.PublicToken != hex.EncodeToString(sum[:]) {
		return nil, false
	}
	return &r, true
}

// ---------- 内部工具 ----------

func newString(s string) *string { return &s }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
