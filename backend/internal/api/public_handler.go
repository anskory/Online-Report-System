package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ce/report-system/backend/internal/model"
	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// PublicHandler 报表公开访问接口（匿名，凭报表级令牌）
type PublicHandler struct {
	engine  *service.QueryEngine
	reports *service.ReportService
	exp     *service.ExportService
	limiter *RateLimiter
	audit   *service.AuditService
	colCfg  *service.ColumnConfigService
}

func NewPublicHandler(engine *service.QueryEngine, reports *service.ReportService, exp *service.ExportService, audit *service.AuditService, colCfg *service.ColumnConfigService) *PublicHandler {
	return &PublicHandler{engine: engine, reports: reports, exp: exp, limiter: NewRateLimiter(30), audit: audit, colCfg: colCfg}
}

// Info GET /api/pub/:publicId/info 校验令牌有效性，返回报表基本信息（不含 SQL）
func (h *PublicHandler) Info(c *gin.Context) {
	report, ok := h.checkToken(c)
	if !ok {
		return
	}
	// 条件配置脱敏输出：不含 optionSql（选项 SQL 由 /options 端点代执行）
	_, params, _, err := h.reports.GetFull(report.ID)
	safeParams := []gin.H{}
	if err == nil {
		for _, p := range params {
			safeParams = append(safeParams, gin.H{
				"id": p.ID, "paramName": p.ParamName, "label": p.Label,
				"widget": p.Widget, "matchType": p.MatchType, "required": p.Required,
				"defaultVal": p.DefaultVal, "defaultMacro": p.DefaultMacro,
				"optionType": p.OptionType, "optionFixed": p.OptionFixed,
			})
		}
	}
	resp.OK(c, gin.H{
		"name":    report.Name,
		"remark":  report.Remark,
		"export":  report.PubAllowExport == 1,
		"timeout": report.QueryTimeout,
		"params":  safeParams,
	})
}

// Query POST /api/pub/:publicId/query 匿名查询
func (h *PublicHandler) Query(c *gin.Context) {
	report, ok := h.checkToken(c)
	if !ok {
		return
	}
	var body queryBody
	_ = c.ShouldBindJSON(&body)
	result, err := h.engine.Execute(c.Request.Context(), report.ID, body.Params, body.Page, body.PageSize, body.SortCol, body.SortDir)
	if err != nil {
		mapEngineError(c, err)
		return
	}
	h.audit.LogAsync(service.AuditEntry{
		LogType: "query", Username: "anonymous", ClientIP: c.ClientIP(),
		ReportID: &report.ID, Detail: "公开访问查询",
	})
	resp.OK(c, result)
}

// Options POST /api/pub/:publicId/options 公开访问的条件选项查询（复用 QueryHandler 公共实现）
func (h *PublicHandler) Options(c *gin.Context) {
	report, ok := h.checkToken(c)
	if !ok {
		return
	}
	qh := NewQueryHandler(h.engine, h.reports, h.audit)
	qh.doOptions(c, report.ID)
}

// Export POST /api/pub/:publicId/export 公开访问导出（仅同步流式，受报表 PubAllowExport 开关控制）
// scope=all（默认）全部数据；scope=page 指定页（page+pageSize）；scope=rows 勾选行（前端回传数据）
func (h *PublicHandler) Export(c *gin.Context) {
	report, ok := h.checkToken(c)
	if !ok {
		return
	}
	if report.PubAllowExport != 1 {
		resp.Err(c, http.StatusForbidden, 1003, "该报表未开放导出")
		return
	}
	var body struct {
		Format   string         `json:"format" binding:"required"` // xlsx | csv
		Params   map[string]any `json:"params"`
		SortCol  string         `json:"sortCol"`
		SortDir  string         `json:"sortDir"`
		Scope    string         `json:"scope"`    // all / page / rows
		Page     int            `json:"page"`     // scope=page：起始页码（从 1 开始）
		PageSize int            `json:"pageSize"` // scope=page：导出行数
		Columns  []string       `json:"columns"`  // scope=rows：列标题
		RowsData [][]any        `json:"rows"`     // scope=rows：行数据
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "format 必填（xlsx / csv）")
		return
	}
	format := strings.ToLower(body.Format)
	if format != "xlsx" && format != "csv" {
		resp.Err(c, http.StatusBadRequest, 2001, "导出格式仅支持 xlsx / csv")
		return
	}

	// 匿名导出仅走同步流式（内存恒定），不产生异步任务文件落盘
	fileName := h.exp.SyncFileName(report.ID, format)
	disposition := "attachment; filename=\"export." + format + "\"; filename*=UTF-8''" + url.PathEscape(fileName)
	c.Header("Content-Disposition", disposition)
	if format == "csv" {
		c.Header("Content-Type", "text/csv; charset=utf-8")
	} else {
		c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	}

	var detail string
	switch body.Scope {
	case "rows":
		// 勾选行导出：数据由前端回传，不再查询数据库
		if len(body.Columns) == 0 || len(body.RowsData) == 0 {
			resp.Err(c, http.StatusBadRequest, 2001, "勾选行数据为空")
			return
		}
		if len(body.RowsData) > 5000 || len(body.Columns) > 1000 {
			resp.Err(c, http.StatusBadRequest, 2001, "勾选行数据超过上限（5000 行 / 1000 列）")
			return
		}
		if _, err := h.exp.WriteRows(c.Writer, format, body.Columns, body.RowsData); err != nil {
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		detail = fmt.Sprintf("公开访问导出勾选行 %d 行 (%s)", len(body.RowsData), format)
	case "page":
		// 指定页导出（当前页 / 指定页共用）
		if body.PageSize < 1 || body.PageSize > 200000 {
			resp.Err(c, http.StatusBadRequest, 2001, "导出条数须在 1 ~ 200000 之间")
			return
		}
		if body.Page < 1 {
			body.Page = 1
		}
		offset := int64(body.Page-1) * int64(body.PageSize)
		if _, _, err := h.exp.StreamScoped(c.Request.Context(), c.Writer, report.ID, format,
			body.Params, body.SortCol, body.SortDir, offset, int64(body.PageSize)); err != nil {
			mapEngineError(c, err)
			return
		}
		detail = fmt.Sprintf("公开访问导出第 %d 页起 %d 行 (%s)", body.Page, body.PageSize, format)
	default:
		if _, _, err := h.exp.StreamSync(c.Request.Context(), c.Writer, report.ID, format, body.Params, body.SortCol, body.SortDir); err != nil {
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		detail = "公开访问导出 " + format
	}
	c.AbortWithStatus(http.StatusOK)
	h.audit.LogAsync(service.AuditEntry{
		LogType: "export", Username: "anonymous", ClientIP: c.ClientIP(),
		ReportID: &report.ID, Detail: detail,
	})
}

// ColCfgGet GET /api/pub/:publicId/column-config 匿名列配置（所有匿名用户共用一套）
func (h *PublicHandler) ColCfgGet(c *gin.Context) {
	report, ok := h.checkToken(c)
	if !ok {
		return
	}
	NewColumnConfigHandler(h.colCfg).PubGet(c, report.ID)
}

// ColCfgSave PUT /api/pub/:publicId/column-config 匿名列配置保存
func (h *PublicHandler) ColCfgSave(c *gin.Context) {
	report, ok := h.checkToken(c)
	if !ok {
		return
	}
	NewColumnConfigHandler(h.colCfg).PubSave(c, report.ID)
}

// ColCfgDelete DELETE /api/pub/:publicId/column-config 匿名列配置重置
func (h *PublicHandler) ColCfgDelete(c *gin.Context) {
	report, ok := h.checkToken(c)
	if !ok {
		return
	}
	NewColumnConfigHandler(h.colCfg).PubDelete(c, report.ID)
}

// checkToken 校验路径 publicId + X-Report-Token 头 + IP 限流
func (h *PublicHandler) checkToken(c *gin.Context) (*model.Report, bool) {
	publicID := c.Param("publicId")
	token := c.GetHeader("X-Report-Token")
	if publicID == "" || token == "" {
		resp.Err(c, http.StatusUnauthorized, 1003, "公开访问令牌缺失")
		return nil, false
	}
	if !h.limiter.Allow(c.ClientIP()) {
		resp.Err(c, http.StatusTooManyRequests, 3002, "访问过于频繁，请稍后再试")
		return nil, false
	}
	report, ok := h.reports.CheckPublicToken(publicID, token)
	if !ok {
		resp.Err(c, http.StatusUnauthorized, 1003, "公开访问令牌无效")
		return nil, false
	}
	return report, true
}
