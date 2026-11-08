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

// Tables GET /api/datasources/:id/tables?keyword=
func (h *MetaHandler) Tables(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	list, err := h.svc.Tables(c.Request.Context(), id, c.Query("keyword"))
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, list)
}

// Columns GET /api/datasources/:id/tables/:name/columns
func (h *MetaHandler) Columns(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	list, err := h.svc.Columns(c.Request.Context(), id, c.Param("name"))
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4002, err.Error())
		return
	}
	resp.OK(c, list)
}
