package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// AuditHandler 操作日志接口（管理员）
type AuditHandler struct {
	audit *service.AuditService
}

func NewAuditHandler(audit *service.AuditService) *AuditHandler {
	return &AuditHandler{audit: audit}
}

// auditExportMaxRows 日志导出的硬上限，避免一次拉出几十万行把内存与浏览器打爆
const auditExportMaxRows = 100000

// List GET /api/system/audit-logs
// 参数：logType、module、username、keyword、success、reportId、datasourceId、from、to、page、pageSize
func (h *AuditHandler) List(c *gin.Context) {
	q, err := parseAuditQuery(c)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, err.Error())
		return
	}
	list, total, err := h.audit.Query(q)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, gin.H{"items": list, "total": total, "page": q.Page, "pageSize": q.PageSize})
}

// Stats GET /api/system/audit-logs/stats?days=7
func (h *AuditHandler) Stats(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	st, err := h.audit.Stats(days)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, st)
}

// Export GET /api/system/audit-logs/export：按当前筛选条件流式导出 CSV（上限 10 万行）
func (h *AuditHandler) Export(c *gin.Context) {
	q, err := parseAuditQuery(c)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, err.Error())
		return
	}

	name := fmt.Sprintf("audit-logs-%s.csv", time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename="+name)

	// UTF-8 BOM：Excel 直接双击打开不乱码（与导出功能保持一致）
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"时间", "类型", "模块", "用户", "IP", "目标", "报表ID", "数据源ID", "耗时(ms)", "结果", "详情", "失败原因"})

	// 分页拉取后逐行写出：不把 10 万行一次性装进内存
	const chunk = 1000
	q.PageSize = chunk
	written := 0
	for page := 1; written < auditExportMaxRows; page++ {
		q.Page = page
		list, _, err := h.audit.Query(q)
		if err != nil {
			break
		}
		for _, r := range list {
			if written >= auditExportMaxRows {
				break
			}
			result := "成功"
			if r.Success == 0 {
				result = "失败"
			}
			_ = w.Write([]string{
				r.CreatedAt.Format("2006-01-02 15:04:05"),
				r.LogType, r.Module, r.Username, r.ClientIP, r.Target,
				ptrNum(r.ReportID), ptrNum(r.DatasourceID),
				strconv.Itoa(r.CostMs), result, r.Detail, r.ErrMsg,
			})
			written++
		}
		if len(list) < chunk {
			break
		}
	}
	w.Flush()
}

// Clean DELETE /api/system/audit-logs?before=YYYY-MM-DD：删除该日期之前的日志
func (h *AuditHandler) Clean(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("before"))
	if raw == "" {
		resp.Err(c, http.StatusBadRequest, 2001, "请指定 before 参数（如 2026-01-01）")
		return
	}
	t, err := parseAuditTime(raw, false)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "before 格式应为 YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss")
		return
	}
	n, err := h.audit.DeleteBefore(t)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, gin.H{"deleted": n, "before": t.Format("2006-01-02 15:04:05")})
}

// ---------- 工具 ----------

func parseAuditQuery(c *gin.Context) (service.AuditQuery, error) {
	q := service.AuditQuery{
		LogType:  service.NormalizeLogType(c.Query("logType")),
		Module:   strings.TrimSpace(c.Query("module")),
		Username: strings.TrimSpace(c.Query("username")),
		Keyword:  strings.TrimSpace(c.Query("keyword")),
	}
	q.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	q.PageSize, _ = strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if q.PageSize <= 0 {
		q.PageSize = 20
	}
	if q.PageSize > 200 { // 页面分页上限；导出走 1000/批（内部接口不受此限制）
		q.PageSize = 200
	}
	q.ReportID, _ = strconv.ParseInt(c.Query("reportId"), 10, 64)
	q.DatasourceID, _ = strconv.ParseInt(c.Query("datasourceId"), 10, 64)

	// success 三态：不传=全部，1=成功，0=失败
	if raw := strings.TrimSpace(c.Query("success")); raw != "" {
		v := raw == "1" || strings.EqualFold(raw, "true")
		q.Success = &v
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		t, err := parseAuditTime(raw, false)
		if err != nil {
			return q, fmt.Errorf("from 格式应为 YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss")
		}
		q.From = &t
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		// 只给日期时补到当天 23:59:59，符合「查到这一天为止」的直觉
		t, err := parseAuditTime(raw, true)
		if err != nil {
			return q, fmt.Errorf("to 格式应为 YYYY-MM-DD 或 YYYY-MM-DD HH:mm:ss")
		}
		q.To = &t
	}
	return q, nil
}

// parseAuditTime 解析日志查询的时间参数，支持三种写法。
// endOfDay=true 且只给了日期时，返回当天 23:59:59。
func parseAuditTime(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	layouts := []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", time.RFC3339}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, raw, time.Local); err == nil {
			return t, nil
		}
	}
	t, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t, nil
}

func ptrNum(p *int64) string {
	if p == nil || *p == 0 {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}
