package api

import (
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// NewRouter 组装路由。接口鉴权统一按权限点（perms）挂载，见 sys_menu 种子数据。
func NewRouter(
	auth *service.AuthService,
	rbac *service.RBACService,
	ds *service.DatasourceService,
	meta *service.MetaService,
	reports *service.ReportService,
	engine *service.QueryEngine,
	audit *service.AuditService,
	exp *service.ExportService,
	dict *service.DictService,
	colCfg *service.ColumnConfigService,
	wizard *service.WizardService,
	jwtSecret string,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	// 为每个请求生成 TraceID 并记录 UA（审计日志里可串联同一次请求）
	r.Use(RequestMeta())

	authHandler := NewAuthHandler(auth, rbac)
	rbacHandler := NewRBACHandler(auth, rbac, audit)
	dsHandler := NewDatasourceHandler(ds)
	metaHandler := NewMetaHandler(meta)
	reportHandler := NewReportHandler(reports)
	queryHandler := NewQueryHandler(engine, reports, audit)
	publicHandler := NewPublicHandler(engine, reports, exp, audit, colCfg)
	exportHandler := NewExportHandler(exp, reports, audit)
	dictHandler := NewDictHandler(dict, ds, audit)
	colCfgHandler := NewColumnConfigHandler(colCfg)
	auditHandler := NewAuditHandler(audit)
	wizardHandler := NewWizardHandler(wizard)

	// 健康检查
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"code": 0, "msg": "ok", "data": gin.H{
			"status": "up",
			// 队列满被丢弃的审计日志条数：持续增长说明日志压力超出队列处理能力
			"auditDropped": audit.Dropped(),
		}})
	})

	// 认证（公开）
	r.POST("/api/auth/login", authHandler.Login)
	r.POST("/api/auth/refresh", authHandler.Refresh)

	// 报表公开访问（匿名 + 令牌）
	pub := r.Group("/api/pub/:publicId")
	{
		pub.GET("/info", publicHandler.Info)
		pub.POST("/query", publicHandler.Query)
		pub.POST("/options", publicHandler.Options)
		pub.POST("/export", publicHandler.Export)
		// 列配置：所有匿名用户共用一套（UserID=0）
		pub.GET("/column-config", publicHandler.ColCfgGet)
		pub.PUT("/column-config", publicHandler.ColCfgSave)
		pub.DELETE("/column-config", publicHandler.ColCfgDelete)
	}

	// 登录后接口
	api := r.Group("/api")
	api.Use(JWTAuth(jwtSecret, rbac))
	{
		// 当前用户：角色、权限点、导航菜单
		api.GET("/auth/me", authHandler.Me)
		api.GET("/menus/my", rbacHandler.MyMenus)
		api.GET("/roles", authHandler.Roles) // 启用中的角色（报表配置页授权下拉）

		api.GET("/datasource-types", dsHandler.Types)
		api.GET("/report-groups", reportHandler.Groups)
		api.GET("/reports", reportHandler.List)
	}

	// 报表查询（登录用户，普通用户需已发布且授权）
	api.POST("/reports/:id/query", queryHandler.Query)
	api.POST("/reports/:id/options", queryHandler.Options)
	api.GET("/reports/:id", reportHandler.Get)

	// 导出（查询授权复用；行数超限自动转异步任务）
	api.POST("/reports/:id/export", exportHandler.Export)
	api.GET("/export-tasks", exportHandler.Tasks)
	api.GET("/export-tasks/:id", exportHandler.Task)
	api.GET("/export-tasks/:id/download", exportHandler.Download)

	// 表格列配置（登录用户按用户隔离，每报表一份）
	api.GET("/reports/:id/column-config", colCfgHandler.Get)
	api.PUT("/reports/:id/column-config", colCfgHandler.Save)
	api.DELETE("/reports/:id/column-config", colCfgHandler.Delete)

	// ---------- 数据源与元数据 ----------
	api.GET("/datasources", RequirePerm("datasource:list"), dsHandler.List)
	api.POST("/datasources", RequirePerm("datasource:create"), dsHandler.Create)
	api.PUT("/datasources/:id", RequirePerm("datasource:update"), dsHandler.Update)
	api.DELETE("/datasources/:id", RequirePerm("datasource:delete"), dsHandler.Delete)
	api.POST("/datasources/:id/test", RequirePerm("datasource:test"), dsHandler.TestByID)
	api.POST("/datasources/test", RequirePerm("datasource:test"), dsHandler.TestInput)
	api.GET("/datasources/:id/tables", RequirePerm("datasource:list"), metaHandler.Tables)
	api.GET("/datasources/:id/tables/:name/columns", RequirePerm("datasource:list"), metaHandler.Columns)
	api.GET("/datasources/:id/tables/:name/indexes", RequirePerm("datasource:list"), metaHandler.Indexes)
	// 手动清空元数据缓存（DDL 后不必等 TTL；也可在配置页点「刷新表清单」）
	api.POST("/datasources/:id/meta/clear", RequirePerm("datasource:update"), metaHandler.ClearCache)

	// ---------- 报表配置 ----------
	api.POST("/report-groups", RequirePerm("report:update"), reportHandler.CreateGroup)
	api.POST("/reports", RequirePerm("report:create"), reportHandler.Save)
	api.PUT("/reports/:id", RequirePerm("report:update"), reportHandler.Save)
	api.DELETE("/reports/:id", RequirePerm("report:delete"), reportHandler.Delete)
	api.POST("/reports/:id/publish", RequirePerm("report:publish"), reportHandler.SetStatus)
	api.POST("/reports/:id/preview", RequirePerm("report:preview"), queryHandler.Preview)
	api.POST("/reports/:id/public", RequirePerm("report:public"), reportHandler.EnablePublic)
	api.PUT("/reports/:id/public", RequirePerm("report:public"), reportHandler.SetPublicExport)
	api.DELETE("/reports/:id/public", RequirePerm("report:public"), reportHandler.DisablePublic)

	// 基于表生成报表草稿（只生成不落库，保存仍走 POST /reports）
	api.POST("/report-wizard/generate", RequirePerm("report:create"), wizardHandler.Generate)

	// ---------- 数据字典 ----------
	api.GET("/dictionaries", RequirePerm("dict:list"), dictHandler.List)
	api.GET("/dictionaries/:id/types", RequirePerm("dict:list"), dictHandler.Types)
	api.POST("/dictionaries/options", RequirePerm("dict:list"), dictHandler.Options)
	api.POST("/dictionaries/test", RequirePerm("dict:list"), dictHandler.Test)
	api.POST("/dictionaries", RequirePerm("dict:create"), dictHandler.Save)
	api.PUT("/dictionaries/:id", RequirePerm("dict:update"), dictHandler.Save)
	api.DELETE("/dictionaries/:id", RequirePerm("dict:delete"), dictHandler.Delete)

	// ---------- 系统管理：用户 ----------
	api.GET("/system/users", RequirePerm("system:user:list"), rbacHandler.Users)
	api.POST("/system/users", RequirePerm("system:user:create"), rbacHandler.CreateUser)
	api.PUT("/system/users/:id", RequirePerm("system:user:update"), rbacHandler.UpdateUser)
	api.DELETE("/system/users/:id", RequirePerm("system:user:delete"), rbacHandler.DeleteUser)
	api.POST("/system/users/:id/reset-password", RequirePerm("system:user:resetPwd"), rbacHandler.ResetPassword)
	api.PUT("/system/users/:id/roles", RequirePerm("system:user:assignRole"), rbacHandler.SetUserRoles)

	// ---------- 系统管理：角色 ----------
	api.GET("/system/roles", RequirePerm("system:role:list"), rbacHandler.Roles)
	api.POST("/system/roles", RequirePerm("system:role:create"), rbacHandler.CreateRole)
	api.PUT("/system/roles/:id", RequirePerm("system:role:update"), rbacHandler.UpdateRole)
	api.DELETE("/system/roles/:id", RequirePerm("system:role:delete"), rbacHandler.DeleteRole)
	api.GET("/system/roles/:id/menus", RequirePerm("system:role:list"), rbacHandler.RoleMenus)
	api.PUT("/system/roles/:id/menus", RequirePerm("system:role:assignMenu"), rbacHandler.SetRoleMenus)
	api.GET("/system/roles/:id/reports", RequirePerm("system:role:list"), rbacHandler.RoleReports)
	api.PUT("/system/roles/:id/reports", RequirePerm("system:role:assignReport"), rbacHandler.SetRoleReports)

	// ---------- 系统管理：菜单 ----------
	api.GET("/system/menus", RequirePerm("system:menu:list"), rbacHandler.MenuTree)
	api.POST("/system/menus", RequirePerm("system:menu:create"), rbacHandler.CreateMenu)
	api.PUT("/system/menus/:id", RequirePerm("system:menu:update"), rbacHandler.UpdateMenu)
	api.DELETE("/system/menus/:id", RequirePerm("system:menu:delete"), rbacHandler.DeleteMenu)

	// ---------- 系统管理：操作日志 ----------
	api.GET("/system/audit-logs", RequirePerm("system:audit:list"), auditHandler.List)
	api.GET("/system/audit-logs/stats", RequirePerm("system:audit:list"), auditHandler.Stats)
	api.GET("/system/audit-logs/export", RequirePerm("system:audit:export"), auditHandler.Export)
	api.DELETE("/system/audit-logs", RequirePerm("system:audit:delete"), auditHandler.Clean)

	return r
}
