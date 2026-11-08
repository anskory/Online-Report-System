package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ExportHandler 导出接口
type ExportHandler struct {
	exp     *service.ExportService
	reports *service.ReportService
	audit   *service.AuditService
}

func NewExportHandler(exp *service.ExportService, reports *service.ReportService, audit *service.AuditService) *ExportHandler {
	return &ExportHandler{exp: exp, reports: reports, audit: audit}
}

// logExport 记录导出审计：成功与失败都写，带报表名快照与耗时。
// 失败尤其要记——导出失败往往是数据量、权限或 SQL 问题，靠它复盘最快。
func (h *ExportHandler) logExport(c *gin.Context, id int64, detail string, err error, cost time.Duration) {
	e := NewAuditEntry(c, "export", "export")
	e.ReportID = &id
	e.Target = h.reports.Name(id)
	e.CostMs = int(cost.Milliseconds())
	e.Detail = detail
	if err != nil {
		e.Success = service.BoolPtr(false)
		e.ErrMsg = clipText(err.Error(), 240)
	}
	h.audit.LogAsync(e)
}

// Export POST /api/reports/:id/export
// scope=all（默认）：行数 ≤ SyncRowLimit 同步流式返回，超过创建异步任务（导出中心下载）
// scope=page：导出指定页（page 起始页 + pageSize 条数），始终同步流式
// scope=rows：导出前端回传的勾选行（columns 标题 + rows 数据），始终同步返回
func (h *ExportHandler) Export(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	if !h.reports.IsAuthorized(CurrentRoles(c), id) {
		resp.Err(c, http.StatusForbidden, 1003, "无权访问该报表")
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
	uid, username := CurrentUser(c)

	// 勾选行导出：数据由前端回传，不再查询数据库
	if body.Scope == "rows" {
		h.exportRows(c, id, format, uid, username, body.Columns, body.RowsData)
		return
	}

	// 分页导出：当前页 / 指定页（始终同步流式）
	if body.Scope == "page" {
		h.exportPage(c, id, format, uid, username, body.Params, body.SortCol, body.SortDir, body.Page, body.PageSize)
		return
	}

	// 全部数据导出：原有流程（行数超限自动转异步任务）
	start := time.Now()
	out, err := h.exp.Export(c.Request.Context(), id, format, body.Params, body.SortCol, body.SortDir, uid, username)
	if err != nil {
		h.logExport(c, id, "导出失败（"+format+"）", err, time.Since(start))
		mapEngineError(c, err)
		return
	}

	if out.Sync {
		// 同步流式导出：日志放到流失效/结束之后再写，
		// 避免「先把成功记上、实际写到一半断了」这种自相矛盾的日志
		fileName := h.exp.SyncFileName(id, format)
		h.setFileHeaders(c, format, fileName)
		if _, _, err := h.exp.StreamSync(c.Request.Context(), c.Writer, id, format, body.Params, body.SortCol, body.SortDir); err != nil {
			// 头已发出无法改状态码，仅中断响应
			h.logExport(c, id, "同步导出中断（"+format+"）", err, time.Since(start))
			_ = c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
		c.AbortWithStatus(http.StatusOK)
		h.logExport(c, id, "同步导出 "+format, nil, time.Since(start))
		return
	}

	// 异步任务
	h.logExport(c, id,
		"创建异步导出任务#"+strconv.FormatInt(out.Task.ID, 10)+" ("+format+")", nil, time.Since(start))
	resp.OK(c, out.Task)
}

// exportRows 勾选行导出：校验数据规模后由服务端直接生成文件
func (h *ExportHandler) exportRows(c *gin.Context, id int64, format string, uid int64, username string, columns []string, rows [][]any) {
	if len(columns) == 0 || len(rows) == 0 {
		resp.Err(c, http.StatusBadRequest, 2001, "勾选行数据为空")
		return
	}
	if len(rows) > 5000 {
		resp.Err(c, http.StatusBadRequest, 2001, "勾选行数超过上限（5000 行）")
		return
	}
	if len(columns) > 1000 {
		resp.Err(c, http.StatusBadRequest, 2001, "列数超过上限（1000 列）")
		return
	}
	fileName := h.exp.SyncFileName(id, format)
	h.setFileHeaders(c, format, fileName)
	start := time.Now()
	if _, err := h.exp.WriteRows(c.Writer, format, columns, rows); err != nil {
		h.logExport(c, id, fmt.Sprintf("导出勾选行失败（%d 行）", len(rows)), err, time.Since(start))
		_ = c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
	c.AbortWithStatus(http.StatusOK)
	h.logExport(c, id, fmt.Sprintf("导出勾选行 %d 行 (%s)", len(rows), format), nil, time.Since(start))
}

// exportPage 指定页导出：page 起始页（从 1 开始）+ pageSize 导出行数，始终同步流式
func (h *ExportHandler) exportPage(c *gin.Context, id int64, format string, uid int64, username string,
	params map[string]any, sortCol, sortDir string, page, pageSize int) {
	if pageSize < 1 || pageSize > 200000 {
		resp.Err(c, http.StatusBadRequest, 2001, "导出条数须在 1 ~ 200000 之间")
		return
	}
	if page < 1 {
		page = 1
	}
	offset := int64(page-1) * int64(pageSize)
	fileName := h.exp.SyncFileName(id, format)
	h.setFileHeaders(c, format, fileName)
	start := time.Now()
	if _, _, err := h.exp.StreamScoped(c.Request.Context(), c.Writer, id, format,
		params, sortCol, sortDir, offset, int64(pageSize)); err != nil {
		h.logExport(c, id, fmt.Sprintf("导出第 %d 页起 %d 行失败（%s）", page, pageSize, format), err, time.Since(start))
		mapEngineError(c, err)
		return
	}
	c.AbortWithStatus(http.StatusOK)
	h.logExport(c, id, fmt.Sprintf("导出第 %d 页起 %d 行 (%s)", page, pageSize, format), nil, time.Since(start))
}

// setFileHeaders 预设下载响应头（文件名 + Content-Type）
func (h *ExportHandler) setFileHeaders(c *gin.Context, format, fileName string) {
	disposition := "attachment; filename=\"export." + format + "\"; filename*=UTF-8''" + url.PathEscape(fileName)
	c.Header("Content-Disposition", disposition)
	if format == "csv" {
		c.Header("Content-Type", "text/csv; charset=utf-8")
	} else {
		c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	}
}

// Tasks GET /api/export-tasks 导出任务列表（管理员全量，普通用户仅本人）
func (h *ExportHandler) Tasks(c *gin.Context) {
	uid, _ := CurrentUser(c)
	list, err := h.exp.ListTasks(IsAdminCtx(c), uid)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询导出任务失败")
		return
	}
	resp.OK(c, list)
}

// Task GET /api/export-tasks/:id 任务详情（含进度）
func (h *ExportHandler) Task(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	uid, _ := CurrentUser(c)
	t, err := h.exp.GetTask(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Err(c, http.StatusNotFound, 2001, "任务不存在")
			return
		}
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	if !IsAdminCtx(c) && t.CreatedBy != uid {
		resp.Err(c, http.StatusForbidden, 1003, "无权查看该任务")
		return
	}
	resp.OK(c, t)
}

// Download GET /api/export-tasks/:id/download 下载导出文件
func (h *ExportHandler) Download(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	uid, _ := CurrentUser(c)
	path, name, err := h.exp.FilePathForDownload(id, IsAdminCtx(c), uid)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, err.Error())
		return
	}
	ext := filepath.Ext(name) // 形如 ".xlsx" / ".csv"；无扩展名时为空串
	c.Header("Content-Disposition", "attachment; filename=\"download"+ext+
		"\"; filename*=UTF-8''"+url.PathEscape(name))
	c.File(path)
}
