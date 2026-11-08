// Package service 业务逻辑层。
// audit.go：审计日志异步落库。
package service

import (
	"log"

	"github.com/ce/report-system/backend/internal/model"
	"gorm.io/gorm"
)

// AuditService 审计日志服务
type AuditService struct {
	db *gorm.DB
}

func NewAuditService(db *gorm.DB) *AuditService {
	return &AuditService{db: db}
}

// AuditEntry 审计日志条目
type AuditEntry struct {
	LogType      string // login / query / export / config_change
	UserID       int64
	Username     string
	ClientIP     string
	ReportID     *int64
	DatasourceID *int64
	Detail       string
	CostMs       int
	Success      bool
}

// LogAsync 异步写入审计日志（不阻塞业务响应）
func (s *AuditService) LogAsync(e AuditEntry) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[audit] 写入审计日志 panic: %v", r)
			}
		}()
		entry := model.AuditLog{
			LogType:      e.LogType,
			UserID:       e.UserID,
			Username:     e.Username,
			ClientIP:     e.ClientIP,
			ReportID:     e.ReportID,
			DatasourceID: e.DatasourceID,
			Detail:       e.Detail,
			CostMs:       e.CostMs,
			Success:      1,
		}
		if !e.Success {
			entry.Success = 0
		}
		if err := s.db.Create(&entry).Error; err != nil {
			log.Printf("[audit] 写入审计日志失败: %v", err)
		}
	}()
}
