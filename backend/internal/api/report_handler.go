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

// ReportHandler 报表配置接口（管理员）
type ReportHandler struct {
	svc *service.ReportService
}

func NewReportHandler(svc *service.ReportService) *ReportHandler {
	return &ReportHandler{svc: svc}
}

// List GET /api/reports
func (h *ReportHandler) List(c *gin.Context) {
	list, err := h.svc.List(CurrentRoles(c))
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询报表列表失败")
		return
	}
	resp.OK(c, list)
}

// Get GET /api/reports/:id 报表详情（普通用户隐藏 SQL/数据源，需已发布且授权）
func (h *ReportHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	roles := CurrentRoles(c)
	r, params, cols, err := h.svc.GetDetail(id, roles)
	if err != nil {
		if errors.Is(err, service.ErrReportNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Err(c, http.StatusNotFound, 2001, "报表不存在或无权访问")
			return
		}
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	out := gin.H{"report": r, "params": params, "columns": cols}
	if service.IsAdmin(roles) {
		out["roleCodes"] = h.svc.RoleCodesOf(id)
	}
	resp.OK(c, out)
}

// Save POST /api/reports 或 PUT /api/reports/:id
func (h *ReportHandler) Save(c *gin.Context) {
	var in service.ReportInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败: "+err.Error())
		return
	}
	uid, username := CurrentUser(c)
	var id int64
	if raw := c.Param("id"); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
			return
		}
	}
	savedID, err := h.svc.Save(id, &in, uid, username)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2002, err.Error())
		return
	}
	resp.OK(c, gin.H{"id": savedID})
}

// Delete DELETE /api/reports/:id
func (h *ReportHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	uid, username := CurrentUser(c)
	if err := h.svc.Delete(id, uid, username); err != nil {
		if errors.Is(err, service.ErrReportNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			resp.Err(c, http.StatusNotFound, 2001, "报表不存在")
			return
		}
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, nil)
}

// SetStatus POST /api/reports/:id/publish  body: {"status": "published"|"disabled"|"draft"}
func (h *ReportHandler) SetStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	var body struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "status 不能为空")
		return
	}
	uid, username := CurrentUser(c)
	if err := h.svc.SetStatus(id, body.Status, uid, username); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, err.Error())
		return
	}
	resp.OK(c, nil)
}

// EnablePublic POST /api/reports/:id/public  开启公开访问，返回一次性明文令牌
func (h *ReportHandler) EnablePublic(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	uid, username := CurrentUser(c)
	publicID, token, err := h.svc.EnablePublic(id, uid, username)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, gin.H{
		"publicId":    publicID,
		"token":       token,
		"previewLink": "/r/" + publicID + "?token=" + token,
	})
}

// DisablePublic DELETE /api/reports/:id/public
func (h *ReportHandler) DisablePublic(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	uid, username := CurrentUser(c)
	if err := h.svc.DisablePublic(id, uid, username); err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, nil)
}

// SetPublicExport PUT /api/reports/:id/public  body: {"allowExport": true|false}
// 设置报表「允许公开导出」开关（控制公开访问页的导出按钮与导出接口）
func (h *ReportHandler) SetPublicExport(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "ID 非法")
		return
	}
	var body struct {
		AllowExport *bool `json:"allowExport"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.AllowExport == nil {
		resp.Err(c, http.StatusBadRequest, 2001, "allowExport 必填（true / false）")
		return
	}
	allow := 0
	if *body.AllowExport {
		allow = 1
	}
	uid, username := CurrentUser(c)
	if err := h.svc.SetPublicExport(id, allow, uid, username); err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, nil)
}

// Groups GET /api/report-groups
func (h *ReportHandler) Groups(c *gin.Context) {
	list, err := h.svc.ListGroups()
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询分组失败")
		return
	}
	resp.OK(c, list)
}

// CreateGroup POST /api/report-groups  body: {"name": "..."}
func (h *ReportHandler) CreateGroup(c *gin.Context) {
	var body struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "分组名不能为空")
		return
	}
	g, err := h.svc.CreateGroup(body.Name)
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, err.Error())
		return
	}
	resp.OK(c, g)
}
