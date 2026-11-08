package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ce/report-system/backend/internal/model"
	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// RBACHandler 用户 / 角色 / 菜单管理接口
type RBACHandler struct {
	auth  *service.AuthService
	rbac  *service.RBACService
	audit *service.AuditService
}

func NewRBACHandler(auth *service.AuthService, rbac *service.RBACService, audit *service.AuditService) *RBACHandler {
	return &RBACHandler{auth: auth, rbac: rbac, audit: audit}
}

// log 记录权限变更审计：统一补 Module 与 Target（名称快照）。
// 以前只写 Detail，日志页「模块 / 目标」两列全空，模块筛选对用户与角色管理完全失效，
// 且删除用户/角色后日志里只剩 #ID，事后无法判断当时动的是谁。
func (h *RBACHandler) log(c *gin.Context, module, target, detail string) {
	e := NewAuditEntry(c, "config_change", module)
	e.Target = target
	e.Detail = detail
	h.audit.LogAsync(e)
}

// userLabel 用户日志快照：显示名（用户名）；都为空时退回 #ID。
// 取不到不阻断主流程——审计日志不该让正常的增删改失败。
func (h *RBACHandler) userLabel(id int64) string {
	u, err := h.auth.GetUser(id)
	if err != nil || u == nil {
		return "#" + strconv.FormatInt(id, 10)
	}
	return userLabelOf(u)
}

// userLabelOf 从已取到的用户对象生成标签（避免重复查库）
func userLabelOf(u *model.User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(u.DisplayName)
	if name == "" {
		return u.Username
	}
	if u.Username == "" || name == u.Username {
		return name
	}
	return name + "（" + u.Username + "）"
}

// roleLabel 角色日志快照：角色名（角色标识），例如「只读用户（viewer）」
func (h *RBACHandler) roleLabel(id int64) string {
	r, err := h.rbac.RoleByID(id)
	if err != nil || r == nil {
		return "#" + strconv.FormatInt(id, 10)
	}
	return roleLabelOf(r)
}

// roleLabelOf 从已取到的角色对象生成标签
func roleLabelOf(r *model.Role) string {
	if r == nil {
		return ""
	}
	if r.RoleName != "" && r.RoleName != r.RoleKey {
		return r.RoleName + "（" + r.RoleKey + "）"
	}
	return r.RoleKey
}

// menuLabel 菜单日志快照：按 ID 在菜单树里找名字；找不到退回 #ID。
// 菜单没有「按 ID 取单条」的现成方法，这里遍历一次全量树（菜单量级很小）。
func (h *RBACHandler) menuLabel(id int64) string {
	tree, err := h.rbac.MenuTree()
	if err != nil {
		return "#" + strconv.FormatInt(id, 10)
	}
	var find func(nodes []*service.MenuNode) string
	find = func(nodes []*service.MenuNode) string {
		for _, n := range nodes {
			if n.ID == id {
				return n.MenuName
			}
			if s := find(n.Children); s != "" {
				return s
			}
		}
		return ""
	}
	if name := find(tree); name != "" {
		return name
	}
	return "#" + strconv.FormatInt(id, 10)
}

// fail 统一错误映射
func fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrUserNotFound), errors.Is(err, service.ErrRoleNotFound), errors.Is(err, service.ErrMenuNotFound):
		resp.Err(c, http.StatusNotFound, 2001, err.Error())
	default:
		resp.Err(c, http.StatusBadRequest, 2002, err.Error())
	}
}

// ---------- 用户 ----------

// Users GET /api/system/users?keyword=
func (h *RBACHandler) Users(c *gin.Context) {
	list, err := h.auth.ListUsers(c.Query("keyword"))
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询用户失败")
		return
	}
	resp.OK(c, list)
}

type userCreateReq struct {
	Username    string   `json:"username" binding:"required"`
	Password    string   `json:"password" binding:"required"`
	DisplayName string   `json:"displayName"`
	RoleKeys    []string `json:"roleKeys"`
	Status      *int     `json:"status"`
}

// CreateUser POST /api/users
func (h *RBACHandler) CreateUser(c *gin.Context) {
	var req userCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "用户名和密码不能为空")
		return
	}
	if len(req.RoleKeys) == 0 {
		resp.Err(c, http.StatusBadRequest, 2001, "请至少选择一个角色")
		return
	}
	status := 1
	if req.Status != nil {
		status = *req.Status
	}
	u, err := h.auth.CreateUser(req.Username, req.Password, req.DisplayName, req.RoleKeys, status)
	if err != nil {
		fail(c, err)
		return
	}
	h.log(c, "user", u.Username, "新建用户 "+u.Username)
	resp.OK(c, gin.H{"id": u.ID})
}

type userUpdateReq struct {
	DisplayName string `json:"displayName"`
	Status      *int   `json:"status"`
}

// UpdateUser PUT /api/users/:id
func (h *RBACHandler) UpdateUser(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	var req userUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	target, err := h.auth.GetUser(id)
	if err != nil {
		fail(c, err)
		return
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = target.DisplayName
	}
	status := target.Status
	if req.Status != nil {
		status = *req.Status
		if status == 0 && h.auth.IsLastAdmin(id) {
			resp.Err(c, http.StatusBadRequest, 2002, "这是最后一个启用的管理员，不能停用")
			return
		}
	}
	if err := h.auth.UpdateUser(id, displayName, status); err != nil {
		fail(c, err)
		return
	}
	label := userLabelOf(target)
	h.log(c, "user", label, "修改用户 "+label+"（状态 "+strconv.Itoa(status)+"）")
	resp.OK(c, nil)
}

// DeleteUser DELETE /api/users/:id
func (h *RBACHandler) DeleteUser(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	uid, _ := CurrentUser(c)
	if uid == id {
		resp.Err(c, http.StatusBadRequest, 2002, "不能删除当前登录账号")
		return
	}
	if h.auth.IsLastAdmin(id) {
		resp.Err(c, http.StatusBadRequest, 2002, "这是最后一个启用的管理员，不能删除")
		return
	}
	// 先取名字：用户删除后日志里只剩 #ID，事后无法判断删的是谁
	label := h.userLabel(id)
	if err := h.auth.DeleteUser(id); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "user", label, "删除用户 "+label)
	resp.OK(c, nil)
}

// ResetPassword POST /api/users/:id/reset-password
func (h *RBACHandler) ResetPassword(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "密码不能为空")
		return
	}
	label := h.userLabel(id)
	if err := h.auth.ResetPassword(id, req.Password); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "user", label, "重置用户 "+label+" 的密码")
	resp.OK(c, nil)
}

// SetUserRoles PUT /api/users/:id/roles
func (h *RBACHandler) SetUserRoles(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	var req struct {
		RoleKeys []string `json:"roleKeys"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	keepAdmin := false
	for _, k := range req.RoleKeys {
		if k == "admin" {
			keepAdmin = true
		}
	}
	if !keepAdmin && h.auth.IsLastAdmin(id) {
		resp.Err(c, http.StatusBadRequest, 2002, "这是最后一个启用的管理员，不能移除其管理员角色")
		return
	}
	label := h.userLabel(id)
	if err := h.rbac.SetUserRoles(id, req.RoleKeys); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "user", label, "调整用户 "+label+" 的角色为 "+strings.Join(req.RoleKeys, ","))
	resp.OK(c, nil)
}

// ---------- 角色 ----------

// Roles GET /api/system/roles（管理页，含停用角色；/api/roles 只返回启用中的角色）
func (h *RBACHandler) Roles(c *gin.Context) {
	list, err := h.rbac.ListRoles()
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询角色失败")
		return
	}
	resp.OK(c, list)
}

// CreateRole POST /api/roles
func (h *RBACHandler) CreateRole(c *gin.Context) {
	var in service.RoleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	r, err := h.rbac.CreateRole(&in)
	if err != nil {
		fail(c, err)
		return
	}
	h.log(c, "role", roleLabelOf(r), "新建角色 "+roleLabelOf(r))
	resp.OK(c, r)
}

// UpdateRole PUT /api/roles/:id
func (h *RBACHandler) UpdateRole(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	var in service.RoleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	if err := h.rbac.UpdateRole(id, &in); err != nil {
		fail(c, err)
		return
	}
	label := h.roleLabel(id)
	h.log(c, "role", label, "修改角色 "+label)
	resp.OK(c, nil)
}

// DeleteRole DELETE /api/roles/:id
func (h *RBACHandler) DeleteRole(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	// 先取名字：角色删除后日志里只剩 #ID
	label := h.roleLabel(id)
	if err := h.rbac.DeleteRole(id); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "role", label, "删除角色 "+label)
	resp.OK(c, nil)
}

// RoleMenus GET /api/roles/:id/menus 角色已授权的菜单 ID
func (h *RBACHandler) RoleMenus(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	role, err := h.rbac.RoleByID(id)
	if err != nil {
		fail(c, err)
		return
	}
	resp.OK(c, h.rbac.RoleMenuIDs(role.RoleKey))
}

// SetRoleMenus PUT /api/roles/:id/menus
func (h *RBACHandler) SetRoleMenus(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	role, err := h.rbac.RoleByID(id)
	if err != nil {
		fail(c, err)
		return
	}
	var req struct {
		MenuIDs []int64 `json:"menuIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	if err := h.rbac.SetRoleMenus(role.RoleKey, req.MenuIDs); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "role", roleLabelOf(role), "调整角色 "+roleLabelOf(role)+" 的菜单权限")
	resp.OK(c, nil)
}

// RoleReports GET /api/roles/:id/reports 角色可见的报表 ID
func (h *RBACHandler) RoleReports(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	role, err := h.rbac.RoleByID(id)
	if err != nil {
		fail(c, err)
		return
	}
	resp.OK(c, h.rbac.RoleReportIDs(role.RoleKey))
}

// SetRoleReports PUT /api/roles/:id/reports
func (h *RBACHandler) SetRoleReports(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	role, err := h.rbac.RoleByID(id)
	if err != nil {
		fail(c, err)
		return
	}
	var req struct {
		ReportIDs []int64 `json:"reportIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	if err := h.rbac.SetRoleReports(role.RoleKey, req.ReportIDs); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "role", roleLabelOf(role), "调整角色 "+roleLabelOf(role)+" 的报表授权")
	resp.OK(c, nil)
}

// ---------- 菜单 ----------

// MenuTree GET /api/menus 全量菜单树（含按钮权限点）
func (h *RBACHandler) MenuTree(c *gin.Context) {
	tree, err := h.rbac.MenuTree()
	if err != nil {
		resp.Err(c, http.StatusInternalServerError, 5000, "查询菜单失败")
		return
	}
	resp.OK(c, tree)
}

// MyMenus GET /api/menus/my 当前用户可见的导航菜单
func (h *RBACHandler) MyMenus(c *gin.Context) {
	resp.OK(c, h.rbac.MenusOf(CurrentRoles(c)))
}

// CreateMenu POST /api/menus
func (h *RBACHandler) CreateMenu(c *gin.Context) {
	var in service.MenuInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	m, err := h.rbac.CreateMenu(&in)
	if err != nil {
		fail(c, err)
		return
	}
	h.log(c, "menu", m.MenuName, "新建菜单 "+m.MenuName)
	resp.OK(c, m)
}

// UpdateMenu PUT /api/menus/:id
func (h *RBACHandler) UpdateMenu(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	var in service.MenuInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数校验失败")
		return
	}
	label := h.menuLabel(id)
	if err := h.rbac.UpdateMenu(id, &in); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "menu", label, "修改菜单 "+label)
	resp.OK(c, nil)
}

// DeleteMenu DELETE /api/menus/:id
func (h *RBACHandler) DeleteMenu(c *gin.Context) {
	id, ok := parseQueryID(c)
	if !ok {
		return
	}
	// 先取名字：菜单删除后日志里只剩 #ID
	label := h.menuLabel(id)
	if err := h.rbac.DeleteMenu(id); err != nil {
		fail(c, err)
		return
	}
	h.log(c, "menu", label, "删除菜单 "+label)
	resp.OK(c, nil)
}
