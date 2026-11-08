package model

import "time"

// User 系统用户（表 sys_user）
type User struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash string     `gorm:"size:100;not null" json:"-"`
	DisplayName  string     `gorm:"size:64;default:''" json:"displayName"`
	Role         string     `gorm:"size:32;default:'user'" json:"role"` // 主角色标识（展示用；授权以 sys_user_role 为准）
	Status       int        `gorm:"default:1" json:"status"`            // 1启用 0禁用
	FailCount    int        `gorm:"default:0" json:"-"`
	LockedUntil  *time.Time `json:"-"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

func (User) TableName() string { return "sys_user" }

// Datasource 业务数据源配置（表 ds_datasource）
type Datasource struct {
	ID             int64     `gorm:"primaryKey" json:"id"`
	Name           string    `gorm:"size:64;uniqueIndex;not null" json:"name"`
	DbType         string    `gorm:"size:20;not null" json:"dbType"` // mysql/postgresql/oracle/sqlserver/sqlite
	Host           string    `gorm:"size:128;default:''" json:"host"`
	Port           int       `json:"port"`
	ServiceName    string    `gorm:"size:128;default:''" json:"serviceName"` // 库名/Oracle服务名/SQLite文件路径
	Username       string    `gorm:"size:64;default:''" json:"username"`
	PasswordCipher []byte    `gorm:"type:blob" json:"-"` // AES-256-GCM 加密
	ExtraConn      string    `gorm:"size:512;default:''" json:"extraConn"`
	PoolMaxOpen    int       `gorm:"default:20" json:"poolMaxOpen"`
	PoolMaxIdle    int       `gorm:"default:5" json:"poolMaxIdle"`
	PoolMaxLifeSec int       `gorm:"default:3600" json:"poolMaxLifeSec"`
	Status         int       `gorm:"default:1" json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func (Datasource) TableName() string { return "ds_datasource" }

// AuditLog 审计日志（表 sys_audit_log）
//
// 字段设计要点：
//   - Module/Target 是冗余快照：报表或数据源被删除后，日志仍能读懂「当时操作的是什么」；
//   - TraceID 由中间件按请求生成，可把同一次请求里的多条日志串起来；
//   - 所有新增字符串列都带 default:''：SQLite 不允许给已有表加「NOT NULL 且无默认值」的列。
type AuditLog struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	LogType      string    `gorm:"size:20;not null;index:idx_audit_type_created,priority:1" json:"logType"` // login/query/export/config_change
	UserID       int64     `gorm:"default:0" json:"userId"`
	Username     string    `gorm:"size:64;default:''" json:"username"`
	ClientIP     string    `gorm:"size:45;default:''" json:"clientIP"`
	Module       string    `gorm:"size:32;default:''" json:"module"`    // report/datasource/dict/system/export
	Target       string    `gorm:"size:128;default:''" json:"target"`   // 目标名称快照（报表名、数据源名…）
	ReportID     *int64    `json:"reportId"`
	DatasourceID *int64    `json:"datasourceId"`
	Detail       string    `gorm:"type:text" json:"detail"`
	ErrMsg       string    `gorm:"size:255;default:''" json:"errMsg"`    // 失败原因摘要（成功时为空）
	UserAgent    string    `gorm:"size:255;default:''" json:"userAgent"`
	TraceID      string    `gorm:"size:36;default:''" json:"traceId"`
	CostMs       int       `gorm:"default:0" json:"costMs"`
	// Success 不能带 default 标签：GORM 对「带默认值的字段」会跳过零值写入，
	// 失败日志的 0 会被丢弃、由库默认值 1 顶上，导致失败被记成成功。
	// 这里改为无默认值 + 写入时显式赋值 0/1。
	Success      int       `json:"success"`
	CreatedAt    time.Time `gorm:"index;index:idx_audit_type_created,priority:2" json:"createdAt"`
}

func (AuditLog) TableName() string { return "sys_audit_log" }
