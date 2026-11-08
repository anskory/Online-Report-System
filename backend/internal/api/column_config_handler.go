package api

import (
	"encoding/json"
	"net/http"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// ColumnConfigHandler 表格列配置接口（列宽/显隐/顺序/冻结）
type ColumnConfigHandler struct {
	cfg *service.ColumnConfigService
}

func NewColumnConfigHandler(cfg *service.ColumnConfigService) *ColumnConfigHandler {
	return &ColumnConfigHandler{cfg: cfg}
}

// Get GET /api/reports/:id/column-config（登录用户，按用户隔离）
func (h *ColumnConfigHandler) Get(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	uid, _ := CurrentUser(c)
	h.doGet(c, id, uid)
}

// Save PUT /api/reports/:id/column-config（登录用户）
func (h *ColumnConfigHandler) Save(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	uid, _ := CurrentUser(c)
	h.doSave(c, id, uid)
}

// Delete DELETE /api/reports/:id/column-config（登录用户，重置用）
func (h *ColumnConfigHandler) Delete(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	uid, _ := CurrentUser(c)
	h.doDelete(c, id, uid)
}

// PubGet 公开访问读取配置（匿名共用 UserID=0 一套）；reportID 由调用方 checkToken 后传入
func (h *ColumnConfigHandler) PubGet(c *gin.Context, reportID int64) {
	h.doGet(c, reportID, 0)
}

// PubSave 公开访问保存配置
func (h *ColumnConfigHandler) PubSave(c *gin.Context, reportID int64) {
	h.doSave(c, reportID, 0)
}

// PubDelete 公开访问删除配置
func (h *ColumnConfigHandler) PubDelete(c *gin.Context, reportID int64) {
	h.doDelete(c, reportID, 0)
}

// ---------- 公共实现 ----------

func (h *ColumnConfigHandler) doGet(c *gin.Context, reportID, userID int64) {
	config, err := h.cfg.Get(reportID, userID)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "读取列配置失败")
		return
	}
	// config 为 JSON 字符串；前端 JSON.parse，空字符串表示从未自定义过
	resp.OK(c, gin.H{"config": config})
}

func (h *ColumnConfigHandler) doSave(c *gin.Context, reportID, userID int64) {
	var body struct {
		Config json.RawMessage `json:"config"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Config) == 0 {
		resp.Err(c, http.StatusBadRequest, 2001, "config 不能为空")
		return
	}
	// 校验是合法 JSON 数组后原样落库
	var arr []any
	if err := json.Unmarshal(body.Config, &arr); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "config 必须是 JSON 数组")
		return
	}
	if err := h.cfg.Save(reportID, userID, string(body.Config)); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, err.Error())
		return
	}
	resp.OK(c, gin.H{"saved": true})
}

func (h *ColumnConfigHandler) doDelete(c *gin.Context, reportID, userID int64) {
	if err := h.cfg.Delete(reportID, userID); err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "删除列配置失败")
		return
	}
	resp.OK(c, gin.H{"deleted": true})
}
