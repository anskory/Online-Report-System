package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/ce/report-system/backend/internal/api"
	"github.com/ce/report-system/backend/internal/config"
	"github.com/ce/report-system/backend/internal/datasource"
	"github.com/ce/report-system/backend/internal/model"
	"github.com/ce/report-system/backend/internal/pkg/crypto"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/ce/report-system/backend/internal/webui"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[启动] 配置加载失败: %v", err)
	}

	// ---------- 系统库初始化 ----------
	db, err := initSysDB(cfg)
	if err != nil {
		log.Fatalf("[启动] 系统库初始化失败: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Datasource{}, &model.AuditLog{},
		&model.ReportGroup{}, &model.Report{}, &model.ReportParam{},
		&model.ReportColumn{}, &model.RoleReport{}, &model.ExportTask{},
		&model.Dict{}, &model.ReportColumnConfig{},
		&model.Role{}, &model.Menu{}, &model.UserRole{}, &model.RoleMenu{},
	); err != nil {
		if strings.Contains(err.Error(), "Cannot add a NOT NULL column") {
			log.Fatalf("[启动] 表结构迁移失败: %v\n[提示] SQLite 不允许给已有表新增「NOT NULL 且无默认值」的列；请给对应模型字段补 gorm:\"...,default:...\"，或删除系统库文件 %s 后重启（注意：会丢失现有数据源/报表/字典配置）", err, cfg.SysDB.DSN)
		}
		log.Fatalf("[启动] 表结构迁移失败: %v", err)
	}

	// ---------- 服务装配 ----------
	auditSvc := service.NewAuditService(db)
	rbacSvc := service.NewRBACService(db)
	authSvc := service.NewAuthService(db, cfg.Security.JWTSecret, cfg.Security.LoginMaxFail, cfg.Security.LoginLockMinutes, auditSvc, rbacSvc)

	// 角色/菜单种子 + 历史用户角色回填（幂等，可重复启动）
	if err := seedRBAC(db); err != nil {
		log.Fatalf("[启动] 角色/菜单初始化失败: %v", err)
	}

	// 初始管理员（仅当用户表为空时创建）：admin / admin123，首次登录后请修改
	var userCount int64
	db.Model(&model.User{}).Count(&userCount)
	if userCount == 0 {
		if _, err := authSvc.CreateUser("admin", "admin123", "系统管理员", []string{"admin"}, 1); err != nil {
			log.Fatalf("[启动] 初始管理员创建失败: %v", err)
		}
		log.Println("[启动] 已创建初始管理员: admin / admin123")
	}

	// 默认报表分组
	var groupCount int64
	db.Model(&model.ReportGroup{}).Count(&groupCount)
	if groupCount == 0 {
		db.Create(&model.ReportGroup{Name: "默认分组"})
	}

	mgr := datasource.NewManager()
	dsSvc := service.NewDatasourceService(db, crypto.DeriveKey(cfg.Security.AESKey), mgr, auditSvc)
	metaSvc := service.NewMetaService(dsSvc)
	reportSvc := service.NewReportService(db, auditSvc)
	dictSvc := service.NewDictService(db)
	queryEngine := service.NewQueryEngine(dsSvc, reportSvc, dictSvc, cfg.Query)
	exportSvc := service.NewExportService(db, queryEngine, cfg.Export)
	colCfgSvc := service.NewColumnConfigService(db)
	exportSvc.StartWorkers()   // 异步导出 Worker（数量可配，默认 2）
	exportSvc.StartCleanup()   // 过期导出文件清理（每 6 小时，默认保留 7 天）

	gin.SetMode(gin.ReleaseMode)
	router := api.NewRouter(authSvc, rbacSvc, dsSvc, metaSvc, reportSvc, queryEngine, auditSvc, exportSvc, dictSvc, colCfgSvc, cfg.Security.JWTSecret)

	// ---------- 前端页面与静态资源（Vite 构建产物，go:embed 嵌入）----------
	// 对外地址为干净路径（/login、/reports、/view?id=1），不带 /ui 前缀也不再带 .html；
	// 未匹配到路由的请求统一落到 NoRoute：先兼容旧地址 301，再交给 webui.Serve 出页面。
	//
	// 启动时打印「嵌入到二进制里的」产物概况：go:embed 是编译期快照，
	// 前端重新构建后没有重新编译后端的话，这里显示的还是旧内容——一眼可辨。
	log.Println("[启动] " + webui.Info())
	if !webui.Built() {
		log.Println("[启动] 前端界面不可用：请先在 frontend/ 执行 npm install && npm run build，再重新编译后端")
	}
	router.GET("/r/:publicId", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", webui.PublicHTML())
	})
	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/login")
	})
	router.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		// 旧地址兼容：/ui/login.html → /login、/ui/ → /login
		// （库里可能还存着老菜单路径，浏览器可能还留着书签）
		if p == "/ui" || strings.HasPrefix(p, "/ui/") {
			// 去掉 /ui 前缀与 .html 后缀：/ui/login.html → login、/ui/assets/x.js → assets/x.js
			name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(p, "/ui"), "/"), ".html")
			if name == "" {
				name = "login"
			}
			redirectPage(c, "/"+name)
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.JSON(http.StatusNotFound, gin.H{"code": 1, "msg": "接口不存在"})
			return
		}
		// 同一页面不允许两套地址：/login.html → /login
		if strings.HasSuffix(p, ".html") {
			redirectPage(c, strings.TrimSuffix(p, ".html"))
			return
		}
		// 接口路径未命中时保持 JSON 语义，不要落成静态文件或 HTML
		if strings.HasPrefix(p, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 1, "msg": "接口不存在"})
			return
		}
		// 前端资源：带扩展名取静态文件，无扩展名一律返回 SPA 宿主页 index.html
		if webui.Serve(c.Writer, c.Request) {
			return
		}
		// 产物缺失时给出「请先构建前端」的提示页，而不是干巴巴的 404
		if !webui.Built() {
			c.Data(http.StatusOK, "text/html; charset=utf-8", webui.BuildHint())
			return
		}
		c.String(http.StatusNotFound, "页面不存在：%s", p)
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("[启动] 在线报表系统服务已启动，监听 %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("[启动] 服务启动失败: %v", err)
	}
}

// initSysDB 初始化系统库（SQLite 默认 / MySQL 可选），SQLite 强制 WAL
func initSysDB(cfg *config.Config) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	}
	switch cfg.SysDB.Driver {
	case "sqlite", "":
		dsn := cfg.SysDB.DSN
		_ = os.MkdirAll(dirOf(dsn), 0o755)
		gormDB, err := gorm.Open(sqlite.Open(dsn), gormCfg)
		if err != nil {
			return nil, err
		}
		// WAL 模式 + busy_timeout：审计写入与查询并发不互斥；PRAGMA 连接后执行，不依赖 DSN 语法
		if sqlDB, err := gormDB.DB(); err == nil {
			_, _ = sqlDB.Exec("PRAGMA journal_mode = WAL")
			_, _ = sqlDB.Exec("PRAGMA busy_timeout = 5000")
			_, _ = sqlDB.Exec("PRAGMA foreign_keys = ON")
		}
		return gormDB, nil
	case "mysql":
		return gorm.Open(mysql.Open(cfg.SysDB.DSN), gormCfg)
	default:
		return nil, fmt.Errorf("不支持的系统库类型: %s", cfg.SysDB.Driver)
	}
}

// redirectPage 301 到干净地址并保留查询串（如 /ui/view.html?id=3 → /view?id=3）
func redirectPage(c *gin.Context, target string) {
	if q := c.Request.URL.RawQuery; q != "" {
		target += "?" + q
	}
	c.Redirect(http.StatusMovedPermanently, target)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
