package service

import (
	"fmt"

	"github.com/ce/report-system/backend/internal/model"
	"gorm.io/gorm"
)

// ColumnConfigService 表格列配置存取。
// 登录用户按 (报表ID, 用户ID) 隔离；匿名用户统一用 UserID=0 共用一套。
type ColumnConfigService struct {
	db *gorm.DB
}

// NewColumnConfigService 创建列配置服务
func NewColumnConfigService(db *gorm.DB) *ColumnConfigService {
	return &ColumnConfigService{db: db}
}

// cfgMaxSize 配置 JSON 大小上限（列数再多也不会超过，防滥用）
const cfgMaxSize = 64 * 1024

// Get 读取配置 JSON，从未设置过返回空字符串。
// 用 Find 而非 First：查不到时 GORM 不会以 Warn 级别打 record not found 日志
// （首次进入页面必然无配置，属正常路径，不应刷日志）
func (s *ColumnConfigService) Get(reportID, userID int64) (string, error) {
	var recs []model.ReportColumnConfig
	err := s.db.Where("report_id = ? AND user_id = ?", reportID, userID).Limit(1).Find(&recs).Error
	if err != nil {
		return "", err
	}
	if len(recs) == 0 {
		return "", nil
	}
	return recs[0].Config, nil
}

// Save 保存配置（upsert）；config 为列配置 JSON，数组顺序即显示顺序。
// 同样用 Find 探测是否存在，避免 First 的 record not found 日志噪音
func (s *ColumnConfigService) Save(reportID, userID int64, config string) error {
	if config == "" {
		return fmt.Errorf("列配置内容为空")
	}
	if len(config) > cfgMaxSize {
		return fmt.Errorf("列配置过大（超过 64KB）")
	}
	var recs []model.ReportColumnConfig
	err := s.db.Where("report_id = ? AND user_id = ?", reportID, userID).Limit(1).Find(&recs).Error
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return s.db.Create(&model.ReportColumnConfig{
			ReportID: reportID, UserID: userID, Config: config,
		}).Error
	}
	return s.db.Model(&model.ReportColumnConfig{}).Where("id = ?", recs[0].ID).
		Update("config", config).Error
}

// Delete 删除配置（重置用）；不存在视为成功
func (s *ColumnConfigService) Delete(reportID, userID int64) error {
	return s.db.Where("report_id = ? AND user_id = ?", reportID, userID).
		Delete(&model.ReportColumnConfig{}).Error
}
