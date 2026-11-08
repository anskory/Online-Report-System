package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DatasourceHandler 数据源管理接口（管理员）
type DatasourceHandler struct {
	svc *service.DatasourceService
}

func NewDatasourceHandler(svc *service.DatasourceService) *DatasourceHandler {
	return &DatasourceHandler{svc: svc}
}

// List GET /api/datasources
func (h *DatasourceHandler) List(c *gin.Context) {
	list, err := h.svc.List()
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询数据源列表失败")
		return
	}
	resp.OK(c, list)
}

// Create POST /api/datasources
func (h *DatasourceHandler) Create(c *gin.Context) {
	var in service.CreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败: "+err.Error())
		return
	}
	_, username := CurrentUser(c)
	ds, err := h.svc.Create(in, username)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, ds)
}

// Update PUT /api/datasources/:id
func (h *DatasourceHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	var in service.CreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败: "+err.Error())
		return
	}
	_, username := CurrentUser(c)
	ds, err := h.svc.Update(id, in, username)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, ds)
}

// Delete DELETE /api/datasources/:id
func (h *DatasourceHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	_, username := CurrentUser(c)
	if err := h.svc.Delete(id, username); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Err(c, http.StatusNotFound, 4001, "数据源不存在")
			return
		}
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, nil)
}

// TestByID POST /api/datasources/:id/test 保存后的连接测试
func (h *DatasourceHandler) TestByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	_, username := CurrentUser(c)
	latency, version, err := h.svc.Test(c.Request.Context(), id, username)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4001, err.Error())
		return
	}
	resp.OK(c, gin.H{"latencyMs": latency, "version": version})
}

// TestInput POST /api/datasources/test 保存前以连接参数测试
func (h *DatasourceHandler) TestInput(c *gin.Context) {
	var in service.CreateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败: "+err.Error())
		return
	}
	_, username := CurrentUser(c)
	latency, version, err := h.svc.TestInput(c.Request.Context(), in, username)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 4001, err.Error())
		return
	}
	resp.OK(c, gin.H{"latencyMs": latency, "version": version})
}

// Types GET /api/datasource-types 支持的数据库类型
func (h *DatasourceHandler) Types(c *gin.Context) {
	resp.OK(c, h.svc.SupportedTypes())
}
