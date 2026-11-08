package api

import (
	"net/http"
	"strconv"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// MetaHandler 元数据扫描接口（管理员）
type MetaHandler struct {
	svc *service.MetaService
}

func NewMetaHandler(svc *service.MetaService) *MetaHandler {
	return &MetaHandler{svc: svc}
}

// Tables GET /api/datasources/:id/tables?keyword=&refresh=1&limit=
// 响应：{items,total,truncated,cached,ageSec}；关键字在缓存的全量清单上做内存过滤
func (h *MetaHandler) Tables(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	// limit 可选：<=0 或非法都回退到配置里的 meta.table_limit
	limit, _ := strconv.Atoi(c.Query("limit"))
	res, err := h.svc.Tables(c.Request.Context(), id, c.Query("keyword"), isTruthy(c.Query("refresh")), limit)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, res)
}

// Columns GET /api/datasources/:id/tables/:name/columns?refresh=1
func (h *MetaHandler) Columns(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	res, err := h.svc.Columns(c.Request.Context(), id, c.Param("name"), isTruthy(c.Query("refresh")))
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, res)
}

// Indexes GET /api/datasources/:id/tables/:name/indexes
func (h *MetaHandler) Indexes(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	list, err := h.svc.Indexes(c.Request.Context(), id, c.Param("name"))
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, list)
}

// isTruthy 解析 refresh 参数：1/true/yes/on 视为真（前端用 ?refresh=1）
func isTruthy(v string) bool {
	switch v {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	}
	return false
}

// ClearCache POST /api/datasources/:id/meta/clear
// 管理员手动清空该数据源的元数据缓存：刚做完 DDL（加表/加字段）时不必等 TTL 过期，
// 也不用去配置页逐个点「刷新表清单」。
func (h *MetaHandler) ClearCache(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	h.svc.Invalidate(id)
	resp.OK(c, gin.H{"datasourceId": id})
}
