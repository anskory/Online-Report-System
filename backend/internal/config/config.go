package config

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Export 导出子系统配置（对应 config.yaml 的 export 段）
type Export struct {
	Dir             string `yaml:"dir"`                // 导出文件目录
	SyncRowLimit    int64  `yaml:"sync_row_limit"`     // 同步导出行数上限（超过走异步任务）
	Workers         int    `yaml:"workers"`            // 异步导出 Worker 数
	RetentionDays   int    `yaml:"retention_days"`     // 导出文件保留天数
	XlsxSheetMaxRow int64  `yaml:"xlsx_sheet_max_row"` // 单 sheet 最大行数
}

// Query 报表查询配置（对应 config.yaml 的 query 段）
type Query struct {
	CountTimeoutSec int `yaml:"count_timeout_sec"` // 统计总数超时（秒），超时降级为「总数未知」，不拖垮数据查询
	CountCacheSec   int `yaml:"count_cache_sec"`   // 统计总数结果缓存（秒），翻页/刷新复用；0=不缓存
}

// Meta 元数据（表/字段清单）缓存配置（对应 config.yaml 的 meta 段）
type Meta struct {
	CacheTTLSec int `yaml:"cache_ttl_sec"` // 缓存有效期（秒）；0=关闭缓存，每次实时扫描业务库
	TableLimit  int `yaml:"table_limit"`   // 单次返回表数量上限，超出截断并回传真实总数；0=不限制
}

// Config 系统配置（对应设计文档第 10 章 config.yaml）
type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	SysDB struct {
		Driver string `yaml:"driver"` // sqlite（默认）| mysql
		DSN    string `yaml:"dsn"`
	} `yaml:"sys_db"`
	Security struct {
		AESKey           string `yaml:"aes_key"`     // 数据源密码加密密钥
		JWTSecret        string `yaml:"jwt_secret"`  // JWT 签名密钥
		LoginMaxFail     int    `yaml:"login_max_fail"`
		LoginLockMinutes int    `yaml:"login_lock_minutes"`
	} `yaml:"security"`
	Log struct {
		Level       string `yaml:"level"`
		SlowQueryMs int    `yaml:"slow_query_ms"`
		AuditDays   int    `yaml:"audit_retention_days"`
		AuditSync   bool   `yaml:"audit_sync"`       // true=审计日志同步写（排障用，会影响响应耗时）
		AuditQueue  int    `yaml:"audit_queue_size"` // 异步写入队列长度，默认 2048
	} `yaml:"log"`
	Export Export `yaml:"export"`
	Query  Query  `yaml:"query"`
	Meta   Meta   `yaml:"meta"`
}

// Load 加载配置文件，环境变量优先（JWT_SECRET / AES_KEY）
func Load(path string) (*Config, error) {
	cfg := &Config{}
	// 先填充默认值
	cfg.Server.Port = 8080
	cfg.SysDB.Driver = "sqlite"
	cfg.SysDB.DSN = "./data/report_sys.db"
	cfg.Security.LoginMaxFail = 5
	cfg.Security.LoginLockMinutes = 10
	cfg.Log.Level = "info"
	cfg.Log.AuditDays = 90
	cfg.Log.AuditQueue = 2048
	cfg.Export.Dir = "./data/exports"
	cfg.Export.SyncRowLimit = 10000
	cfg.Export.Workers = 2
	cfg.Export.RetentionDays = 7
	cfg.Export.XlsxSheetMaxRow = 1000000
	cfg.Query.CountTimeoutSec = 5
	cfg.Query.CountCacheSec = 30
	cfg.Meta.CacheTTLSec = 300
	cfg.Meta.TableLimit = 3000

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件失败: %w", err)
		}
	}
	// 环境变量覆盖
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.Security.JWTSecret = v
	}
	if v := os.Getenv("AES_KEY"); v != "" {
		cfg.Security.AESKey = v
	}
	if cfg.Security.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET 未配置（config.yaml 或环境变量）")
	}
	if cfg.Security.AESKey == "" {
		return nil, fmt.Errorf("AES_KEY 未配置（config.yaml 或环境变量）")
	}
	// 弱密钥告警：随仓库分发的占位密钥一旦上线，等于公开了令牌签名与数据源密码的钥匙
	if isWeakSecret(cfg.Security.JWTSecret) {
		log.Println("[安全警告] jwt_secret 仍为默认占位值，任何拿到源码的人都能伪造登录令牌——请立即通过环境变量 JWT_SECRET 覆盖为随机长密钥")
	}
	if isWeakSecret(cfg.Security.AESKey) {
		log.Println("[安全警告] aes_key 仍为默认占位值，数据源密码的加密形同虚设——请立即通过环境变量 AES_KEY 覆盖为随机长密钥")
	}
	return cfg, nil
}

// isWeakSecret 识别随仓库分发的占位密钥与过短密钥
func isWeakSecret(s string) bool {
	if len(s) < 16 {
		return true
	}
	switch s {
	case "change-me-to-a-random-secret-string", "change-me-to-a-random-aes-key!!":
		return true
	}
	return false
}
