package model

import "time"

// ReportGroup 报表分组（表 rpt_group）
type ReportGroup struct {
	ID     int64  `gorm:"primaryKey" json:"id"`
	Name   string `gorm:"size:64;not null" json:"name"`
	SortNo int    `gorm:"default:0" json:"sortNo"`
}

func (ReportGroup) TableName() string { return "rpt_group" }

// Report 报表主表（表 rpt_report）
type Report struct {
	ID            int64     `gorm:"primaryKey" json:"id"`
	Name          string    `gorm:"size:128;not null" json:"name"`
	GroupID       int64     `gorm:"not null" json:"groupId"`
	DatasourceID  int64     `gorm:"not null" json:"datasourceId"`
	SQLText       string    `gorm:"type:text;not null" json:"sqlText"`
	Status        string    `gorm:"size:10;default:'draft'" json:"status"` // draft/published/disabled
	QueryTimeout  int       `gorm:"default:30" json:"queryTimeout"`        // 秒
	RowLimit      int       `gorm:"default:100000" json:"rowLimit"`
	ExportLimit   int64     `gorm:"default:5000000" json:"exportLimit"`
	DefaultOrder  string    `gorm:"size:128;default:''" json:"defaultOrder"` // 结果列名
	PublicID      *string   `gorm:"size:12;uniqueIndex" json:"publicId"`
	IsPublic      int       `gorm:"default:0" json:"isPublic"`
	PublicToken   string    `gorm:"size:64;default:''" json:"-"` // SHA-256 哈希
	PubAllowExport int      `gorm:"default:0" json:"pubAllowExport"`
	Remark        string    `gorm:"size:255;default:''" json:"remark"`
	CreatedBy     int64     `json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (Report) TableName() string { return "rpt_report" }

// ReportParam 报表查询条件配置（表 rpt_report_param）
type ReportParam struct {
	ID           int64  `gorm:"primaryKey" json:"id"`
	ReportID     int64  `gorm:"index;not null" json:"reportId"`
	ParamName    string `gorm:"size:64;not null" json:"paramName"`  // 对应 SQL 中 :name（英文标识符，作绑定变量）
	ColumnName   string `gorm:"size:128;default:''" json:"columnName"` // 自动装配的过滤列名；空=用 ParamName。支持中文等结果集列名
	Label        string `gorm:"size:64;not null" json:"label"`
	Widget       string `gorm:"size:20;not null" json:"widget"`   // text/number/select/multiSelect/date/dateTime/dateRange
	MatchType    string `gorm:"size:16;not null" json:"matchType"` // eq/like/in/range
	Required     int    `gorm:"default:0" json:"required"`
	DefaultVal   string `gorm:"size:1024;default:''" json:"defaultVal"` // 多选下拉的默认值是逗号拼接的多个键值，放宽长度
	DefaultMacro string `gorm:"size:32;default:''" json:"defaultMacro"` // today/yesterday/last7days/last30days/thisMonth
	OptionType   string `gorm:"size:10;default:'fixed'" json:"optionType"` // fixed/sql/dict
	OptionFixed  string `gorm:"type:text" json:"optionFixed"`              // JSON: [{"value":"","label":""}]
	OptionSQL    string `gorm:"type:text" json:"optionSql"`
	OptionDict   string `gorm:"size:64;default:''" json:"optionDict"` // optionType=dict 时的字典编码
	OptionDictType string `gorm:"size:64;default:''" json:"optionDictType"` // 字典类型筛选（可选，空=全部类型）
	SortNo       int    `gorm:"default:0" json:"sortNo"`
}

func (ReportParam) TableName() string { return "rpt_report_param" }

// ReportColumn 报表展示列配置（表 rpt_report_column）
type ReportColumn struct {
	ID         int64  `gorm:"primaryKey" json:"id"`
	ReportID   int64  `gorm:"index;not null" json:"reportId"`
	ColName    string `gorm:"size:64;not null" json:"colName"`
	Label      string `gorm:"size:64;default:''" json:"label"`
	ColType    string `gorm:"size:32;default:''" json:"colType"`
	Width      int    `gorm:"default:0" json:"width"`
	Hidden     int    `gorm:"default:0" json:"hidden"`
	FmtType    string `gorm:"size:16;default:''" json:"fmtType"` // date/datetime/number/thousand/decimal
	FmtPattern string `gorm:"size:32;default:''" json:"fmtPattern"`
	DictCode   string `gorm:"size:64;default:''" json:"dictCode"`  // 绑定字典编码（列翻译）
	DictType   string `gorm:"size:64;default:''" json:"dictType"`  // 字典类型筛选（可选，空=全部）
	ListFmt    string `gorm:"size:16;default:''" json:"listFmt"`   // 列表显示：value/label/both（空=label）
	ExportFmt  string `gorm:"size:16;default:''" json:"exportFmt"` // 导出显示：value/label/both（空=label）
	SortNo     int    `gorm:"default:0" json:"sortNo"`
}

func (ReportColumn) TableName() string { return "rpt_report_column" }

// RoleReport 角色-报表可见权限（表 rpt_role_report）
type RoleReport struct {
	ID       int64  `gorm:"primaryKey" json:"id"`
	RoleCode string `gorm:"size:32;not null;uniqueIndex:uk_role_report" json:"roleCode"`
	ReportID int64  `gorm:"not null;uniqueIndex:uk_role_report" json:"reportId"`
}

func (RoleReport) TableName() string { return "rpt_role_report" }

// ReportColumnConfig 用户级表格列配置（表 rpt_column_config）。
// 记录列的显示顺序/宽度/显隐/冻结；UserID=0 为匿名公用配置（公开访问用户共享一套）
type ReportColumnConfig struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	ReportID  int64     `gorm:"not null;uniqueIndex:uk_report_user,priority:1" json:"reportId"`
	UserID    int64     `gorm:"not null;uniqueIndex:uk_report_user,priority:2" json:"userId"` // 0=匿名公用
	Config    string    `gorm:"type:text" json:"config"` // JSON: [{name,width,visible,frozen}]，数组顺序即显示顺序
	UpdatedAt time.Time `json:"updatedAt"`
}

func (ReportColumnConfig) TableName() string { return "rpt_column_config" }
