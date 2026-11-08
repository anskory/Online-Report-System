package api

import (
	"errors"
	"net/http"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// AuthHandler 认证与当前用户接口
type AuthHandler struct {
	auth *service.AuthService
	rbac *service.RBACService
}

func NewAuthHandler(auth *service.AuthService, rbac *service.RBACService) *AuthHandler {
	return &AuthHandler{auth: auth, rbac: rbac}
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login POST /api/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "用户名和密码不能为空")
		return
	}
	result, err := h.auth.Login(req.Username, req.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccountLocked):
			resp.Err(c, http.StatusLocked, 1002, err.Error())
		case errors.Is(err, service.ErrBadCredential), errors.Is(err, service.ErrAccountBanned):
			resp.Err(c, http.StatusUnauthorized, 1001, err.Error())
		default:
			resp.Err(c, http.StatusUnauthorized, 1001, err.Error())
		}
		return
	}
	resp.OK(c, result)
}

// Refresh POST /api/auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refreshToken" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "refreshToken 不能为空")
		return
	}
	token, err := h.auth.Refresh(req.RefreshToken)
	if err != nil {
		resp.Err(c, http.StatusUnauthorized, 1001, err.Error())
		return
	}
	resp.OK(c, gin.H{"accessToken": token})
}

// Me GET /api/auth/me 当前用户 + 角色 + 权限点 + 导航菜单（前端一次拉齐）
func (h *AuthHandler) Me(c *gin.Context) {
	uid, _ := CurrentUser(c)
	u, err := h.auth.GetUser(uid)
	if err != nil {
		resp.Err(c, http.StatusUnauthorized, 1001, "用户不存在或已删除")
		return
	}
	roles := CurrentRoles(c)
	resp.OK(c, gin.H{
		"user":  u,
		"roles": h.rbac.RoleBriefsOf(roles),
		"perms": service.PermList(CurrentPerms(c)),
		"menus": h.rbac.MenusOf(roles),
	})
}

// Roles GET /api/roles 启用中的角色列表（报表配置页的「可见角色」下拉）
func (h *AuthHandler) Roles(c *gin.Context) {
	roles, err := h.rbac.ListActiveRoles()
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询角色失败")
		return
	}
	resp.OK(c, roles)
}
