package model

import "time"

// ExportTask 异步导出任务（表 exp_export_task）
type ExportTask struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	ReportID       int64     `gorm:"index;not null" json:"reportId"`
	ReportName     string    `gorm:"size:128;not null" json:"reportName"`
	Format         string    `gorm:"size:10;not null" json:"format"`   // xlsx | csv
	Status         string    `gorm:"size:10;not null;default:'queued';index" json:"status"` // queued/running/done/failed
	Progress       int       `gorm:"default:0" json:"progress"`        // 0-100
	TotalRows      int64     `json:"totalRows"`
	FilePath       string    `gorm:"size:255;default:''" json:"filePath"`
	FileName       string    `gorm:"size:255;default:''" json:"fileName"`
	FileSize       int64     `gorm:"default:0" json:"fileSize"`
	ErrorMsg       string    `gorm:"size:500;default:''" json:"errorMsg"`
	ParamsSnapshot string    `gorm:"type:text" json:"-"`               // 查询参数 JSON 快照
	CreatedBy      int64     `gorm:"index" json:"createdBy"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
}

func (ExportTask) TableName() string { return "exp_export_task" }
