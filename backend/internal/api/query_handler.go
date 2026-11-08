package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// queryBody 查询请求体
type queryBody struct {
	Params   map[string]any `json:"params"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
	SortCol  string         `json:"sortCol"`
	SortDir  string         `json:"sortDir"`
}

// parseQueryID 解析路径中的报表 ID
func parseQueryID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return 0, false
	}
	return id, true
}

// mapEngineError 引擎错误 → HTTP 响应
func mapEngineError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSortInvalid):
		resp.Err(c, http.StatusBadRequest, 2003, err.Error())
	case errors.Is(err, service.ErrParamRequired),
		errors.Is(err, service.ErrParamInvalid),
		errors.Is(err, service.ErrParamMissing):
		resp.Err(c, http.StatusBadRequest, 2001, err.Error())
	case errors.Is(err, service.ErrReportNotFound), errors.Is(err, service.ErrDSNNotFound):
		resp.Err(c, http.StatusNotFound, 2001, err.Error())
	case errors.Is(err, service.ErrSQLRequired):
		resp.Err(c, http.StatusBadRequest, 2002, err.Error())
	default:
		resp.Err(c, http.StatusInternalServerError, 3001, err.Error())
	}
}

// QueryHandler 报表查询接口
type QueryHandler struct {
	engine  *service.QueryEngine
	reports *service.ReportService
	audit   *service.AuditService
}

func NewQueryHandler(engine *service.QueryEngine, reports *service.ReportService, audit *service.AuditService) *QueryHandler {
	return &QueryHandler{engine: engine, reports: reports, audit: audit}
}

// Query POST /api/reports/:id/query（登录用户，需已发布且角色授权；管理员不限）
func (h *QueryHandler) Query(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	if !h.reports.IsAuthorized(CurrentRoles(c), id) {
		resp.Err(c, http.StatusForbidden, 1003, "无权访问该报表")
		return
	}
	var body queryBody
	_ = c.ShouldBindJSON(&body) // 允许空 body（全部走默认值）
	start := time.Now()
	result, err := h.engine.Execute(c.Request.Context(), id, body.Params, body.Page, body.PageSize, body.SortCol, body.SortDir)
	cost := int(time.Since(start).Milliseconds())

	// 成功与失败都留痕：查询失败（SQL 报错、超时、条件非法）恰恰是最需要复盘的时刻，
	// 只记成功等于把最有价值的日志丢掉了。Target 存报表名快照，报表改名后仍能对上。
	e := NewAuditEntry(c, "query", "report")
	e.ReportID = &id
	e.Target = h.reports.Name(id)
	e.CostMs = cost
	if err != nil {
		e.Success = service.BoolPtr(false)
		e.ErrMsg = clipText(err.Error(), 240)
		e.Detail = fmt.Sprintf("报表查询失败（第 %d 页）", pageOrDefault(body.Page))
		h.audit.LogAsync(e)
		mapEngineError(c, err)
		return
	}
	e.Detail = fmt.Sprintf("报表查询 第 %d 页 / 每页 %d 行", result.Page, result.PageSize)
	h.audit.LogAsync(e)
	resp.OK(c, result)
}

func pageOrDefault(p int) int {
	if p <= 0 {
		return 1
	}
	return p
}

// clipText 按「字符」截断长文本（错误信息常带中文与 SQL 片段），
// 避免超出 audit_log 字段长度导致整条日志写入失败。
func clipText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// Options POST /api/reports/:id/options 执行条件项的选项 SQL
func (h *QueryHandler) Options(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	if !h.reports.IsAuthorized(CurrentRoles(c), id) {
		resp.Err(c, http.StatusForbidden, 1003, "无权访问该报表")
		return
	}
	h.doOptions(c, id)
}

// doOptions Options 的公共实现（登录与公开访问共用）
func (h *QueryHandler) doOptions(c *gin.Context, id int64) {
	var body struct {
		Param  string         `json:"param" binding:"required"`
		Params map[string]any `json:"params"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "param 不能为空")
		return
	}
	rows, err := h.engine.Options(c.Request.Context(), id, body.Param, body.Params)
	if err != nil {
		mapEngineError(c, err)
		return
	}
	resp.OK(c, rows)
}

// Preview POST /api/reports/:id/preview（管理员，草稿可预览，最多 100 行）
func (h *QueryHandler) Preview(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	var body queryBody
	_ = c.ShouldBindJSON(&body)
	result, err := h.engine.Execute(c.Request.Context(), id, body.Params, 1, 100, body.SortCol, body.SortDir)
	if err != nil {
		mapEngineError(c, err)
		return
	}
	resp.OK(c, result)
}
