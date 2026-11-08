package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/ce/report-system/backend/internal/model"
	"gorm.io/gorm"
)

// 角色/菜单相关业务错误
var (
	ErrRoleNotFound    = errors.New("角色不存在")
	ErrRoleKeyExists   = errors.New("角色标识已存在")
	ErrRoleKeyInvalid  = errors.New("角色标识只能是小写字母开头的字母/数字/下划线，长度 2-32")
	ErrRoleKeyLocked   = errors.New("角色标识创建后不可修改")
	ErrRoleBuiltin     = errors.New("内置角色不可删除")
	ErrRoleInUse       = errors.New("角色已分配给用户，请先解除关联")
	ErrMenuNotFound    = errors.New("菜单不存在")
	ErrMenuHasChildren = errors.New("存在下级菜单，请先删除下级")
	ErrMenuParentSelf  = errors.New("上级菜单不能是自己或自己的下级")
	ErrMenuTypeInvalid = errors.New("菜单类型只能是 M/C/F")
)

var roleKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// RBACService 角色-菜单-权限服务。
// 「角色 → 权限点集合」按角色做了内存缓存，任何授权变更后调用 Invalidate 失效重载。
type RBACService struct {
	db *gorm.DB

	mu        sync.RWMutex
	permCache map[string]map[string]struct{}
}

func NewRBACService(db *gorm.DB) *RBACService {
	return &RBACService{db: db, permCache: map[string]map[string]struct{}{}}
}

// Invalidate 清空权限缓存（角色、菜单、授权关系变更后必须调用）
func (s *RBACService) Invalidate() {
	s.mu.Lock()
	s.permCache = map[string]map[string]struct{}{}
	s.mu.Unlock()
}

// IsAdmin 判断角色集合是否包含超级管理员（admin 拥有隐式全量权限）
func IsAdmin(roles []string) bool {
	for _, r := range roles {
		if r == "admin" {
			return true
		}
	}
	return false
}

// ---------- 权限点解析 ----------

// RolesOf 用户当前生效的角色标识列表（按角色排序）。
// 关联表存在但角色被停用时返回空（停用即失权）；无任何关联时回退 sys_user.role 兼容历史数据。
// 用户被禁用（status<>1）时同样返回空：JWTAuth 每次请求都调本方法，
// 禁用账号必须立即失权，不能等到令牌自然过期。
func (s *RBACService) RolesOf(userID int64) []string {
	var keys []string
	s.db.Table("sys_user_role AS ur").
		Joins("JOIN sys_role AS r ON r.role_key = ur.role_key").
		Joins("JOIN sys_user AS u ON u.id = ur.user_id").
		Where("ur.user_id = ? AND r.status = 1 AND u.status = 1", userID).
		Order("r.sort_no, ur.id").
		Pluck("ur.role_key", &keys)
	if len(keys) > 0 {
		return keys
	}
	var total int64
	s.db.Model(&model.UserRole{}).Where("user_id = ?", userID).Count(&total)
	if total > 0 {
		return nil
	}
	var u model.User
	if err := s.db.Select("role", "status").First(&u, userID).Error; err == nil && u.Role != "" && u.Status == 1 {
		return []string{u.Role}
	}
	return nil
}

// permsOfRole 单个角色的权限点集合（带缓存）
func (s *RBACService) permsOfRole(roleKey string) map[string]struct{} {
	s.mu.RLock()
	cached, ok := s.permCache[roleKey]
	s.mu.RUnlock()
	if ok {
		return cached
	}

	set := map[string]struct{}{}
	if roleKey == "admin" {
		set["*"] = struct{}{} // 超级管理员：通配全部权限
	} else {
		var perms []string
		s.db.Table("sys_menu AS m").
			Joins("JOIN sys_role_menu AS rm ON rm.menu_id = m.id").
			Where("rm.role_key = ? AND m.status = 1 AND m.perms <> ''", roleKey).
			Pluck("m.perms", &perms)
		for _, p := range perms {
			for _, one := range strings.Split(p, ",") {
				if one = strings.TrimSpace(one); one != "" {
					set[one] = struct{}{}
				}
			}
		}
	}

	s.mu.Lock()
	s.permCache[roleKey] = set
	s.mu.Unlock()
	return set
}

// PermsOf 多角色权限点并集
func (s *RBACService) PermsOf(roles []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, r := range roles {
		for p := range s.permsOfRole(r) {
			out[p] = struct{}{}
		}
	}
	return out
}

// PermList 权限点集合转切片（含 * 时直接返回 ["*"]，便于前端判断）
func PermList(set map[string]struct{}) []string {
	if _, all := set["*"]; all {
		return []string{"*"}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	return out
}

// HasPerm 判断角色集合是否拥有某权限点（admin 恒为 true）
func (s *RBACService) HasPerm(roles []string, perm string) bool {
	set := s.PermsOf(roles)
	if _, all := set["*"]; all {
		return true
	}
	_, ok := set[perm]
	return ok
}

// ---------- 菜单树 ----------

// MenuNode 菜单树节点
type MenuNode struct {
	ID       int64       `json:"id"`
	ParentID int64       `json:"parentId"`
	MenuName string      `json:"menuName"`
	MenuType string      `json:"menuType"`
	Path     string      `json:"path"`
	Perms    string      `json:"perms"`
	Icon     string      `json:"icon"`
	SortNo   int         `json:"sortNo"`
	Visible  int         `json:"visible"`
	Status   int         `json:"status"`
	Children []*MenuNode `json:"children"`
}

func buildMenuTree(list []model.Menu) []*MenuNode {
	index := make(map[int64]*MenuNode, len(list))
	for i := range list {
		m := list[i]
		index[m.ID] = &MenuNode{
			ID: m.ID, ParentID: m.ParentID, MenuName: m.MenuName, MenuType: m.MenuType,
			Path: m.Path, Perms: m.Perms, Icon: m.Icon, SortNo: m.SortNo,
			Visible: m.Visible, Status: m.Status, Children: []*MenuNode{},
		}
	}
	roots := make([]*MenuNode, 0, 8)
	for i := range list {
		n := index[list[i].ID]
		if p, ok := index[n.ParentID]; ok && n.ParentID != 0 {
			p.Children = append(p.Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	return roots
}

// MenusOf 角色可见的导航菜单（仅 M/C，已停用/隐藏的节点不出现；空目录自动剔除）
func (s *RBACService) MenusOf(roles []string) []*MenuNode {
	if len(roles) == 0 {
		return []*MenuNode{}
	}
	var list []model.Menu
	q := s.db.Where("menu_type IN ?", []string{"M", "C"}).Where("status = 1 AND visible = 1")
	if !IsAdmin(roles) {
		q = q.Where("id IN (?)", s.db.Model(&model.RoleMenu{}).Select("menu_id").Where("role_key IN ?", roles))
	}
	if err := q.Order("sort_no, id").Find(&list).Error; err != nil {
		return []*MenuNode{}
	}
	roots := buildMenuTree(list)
	out := make([]*MenuNode, 0, len(roots))
	for _, n := range roots {
		if n.MenuType == "M" && len(n.Children) == 0 {
			continue
		}
		if n.MenuType == "C" && n.Path == "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// MenuTree 全量菜单树（含按钮权限点），供菜单管理页与角色授权树使用
func (s *RBACService) MenuTree() ([]*MenuNode, error) {
	var list []model.Menu
	if err := s.db.Order("sort_no, id").Find(&list).Error; err != nil {
		return nil, err
	}
	return buildMenuTree(list), nil
}

// ---------- 角色 ----------

// RoleBrief 角色摘要（用户列表/登录信息回显）
type RoleBrief struct {
	RoleKey  string `json:"roleKey"`
	RoleName string `json:"roleName"`
}

// ListRoles 角色列表（含停用角色，管理页需要看到全部）
func (s *RBACService) ListRoles() ([]model.Role, error) {
	var list []model.Role
	err := s.db.Order("sort_no, id").Find(&list).Error
	return list, err
}

// RoleByID 按主键取角色
func (s *RBACService) RoleByID(id int64) (*model.Role, error) {
	var r model.Role
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRoleNotFound
		}
		return nil, err
	}
	return &r, nil
}

// ListActiveRoles 启用中的角色（报表配置页授权下拉用）
func (s *RBACService) ListActiveRoles() ([]RoleBrief, error) {
	var list []RoleBrief
	err := s.db.Model(&model.Role{}).Select("role_key, role_name").
		Where("status = 1").Order("sort_no, id").Scan(&list).Error
	return list, err
}

// RoleBriefsOf 按角色标识取显示名（登录信息/用户列表回显）
func (s *RBACService) RoleBriefsOf(keys []string) []RoleBrief {
	if len(keys) == 0 {
		return []RoleBrief{}
	}
	list := make([]RoleBrief, 0, len(keys))
	s.db.Model(&model.Role{}).Select("role_key, role_name").
		Where("role_key IN ?", keys).Order("sort_no, id").Scan(&list)
	return list
}

// RoleNameMap 角色标识 → 显示名（批量回显，避免 N+1 查询）
func (s *RBACService) RoleNameMap() map[string]string {
	var rows []model.Role
	s.db.Select("role_key", "role_name").Find(&rows)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.RoleKey] = r.RoleName
	}
	return out
}

// RoleInput 角色保存入参（Status 用指针区分「未传」与「显式停用」）
type RoleInput struct {
	RoleKey  string `json:"roleKey"`
	RoleName string `json:"roleName"`
	SortNo   int    `json:"sortNo"`
	Status   *int   `json:"status"`
	Remark   string `json:"remark"`
}

// CreateRole 新建角色
func (s *RBACService) CreateRole(in *RoleInput) (*model.Role, error) {
	key := strings.TrimSpace(in.RoleKey)
	name := strings.TrimSpace(in.RoleName)
	if name == "" {
		return nil, errors.New("角色名称不能为空")
	}
	if !roleKeyPattern.MatchString(key) {
		return nil, ErrRoleKeyInvalid
	}
	var cnt int64
	s.db.Model(&model.Role{}).Where("role_key = ?", key).Count(&cnt)
	if cnt > 0 {
		return nil, ErrRoleKeyExists
	}
	status := 1
	if in.Status != nil {
		status = *in.Status
	}
	r := &model.Role{RoleKey: key, RoleName: name, SortNo: in.SortNo, Status: status, Remark: in.Remark}
	if err := s.db.Create(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateRole 更新角色（角色标识不可修改，避免已授权数据悬空）
func (s *RBACService) UpdateRole(id int64, in *RoleInput) error {
	var r model.Role
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRoleNotFound
		}
		return err
	}
	if key := strings.TrimSpace(in.RoleKey); key != "" && key != r.RoleKey {
		return ErrRoleKeyLocked
	}
	if name := strings.TrimSpace(in.RoleName); name != "" {
		r.RoleName = name
	}
	r.SortNo = in.SortNo
	if in.Status != nil {
		r.Status = *in.Status
	}
	r.Remark = in.Remark
	if err := s.db.Save(&r).Error; err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

// DeleteRole 删除角色（内置角色与在用角色不可删）
func (s *RBACService) DeleteRole(id int64) error {
	var r model.Role
	if err := s.db.First(&r, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRoleNotFound
		}
		return err
	}
	if r.Builtin == 1 {
		return ErrRoleBuiltin
	}
	var used int64
	s.db.Model(&model.UserRole{}).Where("role_key = ?", r.RoleKey).Count(&used)
	if used > 0 {
		return ErrRoleInUse
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.Role{}, id).Error; err != nil {
			return err
		}
		if err := tx.Where("role_key = ?", r.RoleKey).Delete(&model.RoleMenu{}).Error; err != nil {
			return err
		}
		return tx.Where("role_code = ?", r.RoleKey).Delete(&model.RoleReport{}).Error
	})
	if err == nil {
		s.Invalidate()
	}
	return err
}

// RoleMenuIDs 角色已授权的菜单 ID
func (s *RBACService) RoleMenuIDs(roleKey string) []int64 {
	ids := make([]int64, 0, 32)
	s.db.Model(&model.RoleMenu{}).Where("role_key = ?", roleKey).Order("menu_id").Pluck("menu_id", &ids)
	return ids
}

// SetRoleMenus 全量替换角色的菜单授权
func (s *RBACService) SetRoleMenus(roleKey string, menuIDs []int64) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_key = ?", roleKey).Delete(&model.RoleMenu{}).Error; err != nil {
			return err
		}
		seen := map[int64]struct{}{}
		for _, id := range menuIDs {
			if id <= 0 {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			if err := tx.Create(&model.RoleMenu{RoleKey: roleKey, MenuID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.Invalidate()
	}
	return err
}

// RoleReportIDs 角色可见的报表 ID
func (s *RBACService) RoleReportIDs(roleKey string) []int64 {
	ids := make([]int64, 0, 8)
	s.db.Model(&model.RoleReport{}).Where("role_code = ?", roleKey).Order("report_id").Pluck("report_id", &ids)
	return ids
}

// SetRoleReports 全量替换角色的报表可见授权（与报表配置页写的是同一张 rpt_role_report）
func (s *RBACService) SetRoleReports(roleKey string, reportIDs []int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_code = ?", roleKey).Delete(&model.RoleReport{}).Error; err != nil {
			return err
		}
		seen := map[int64]struct{}{}
		for _, id := range reportIDs {
			if id <= 0 {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			if err := tx.Create(&model.RoleReport{RoleCode: roleKey, ReportID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------- 菜单 ----------

// MenuInput 菜单保存入参
type MenuInput struct {
	ParentID int64  `json:"parentId"`
	MenuName string `json:"menuName"`
	MenuType string `json:"menuType"`
	Path     string `json:"path"`
	Perms    string `json:"perms"`
	Icon     string `json:"icon"`
	SortNo   int    `json:"sortNo"`
	Visible  *int   `json:"visible"`
	Status   *int   `json:"status"`
}

func (in *MenuInput) validate() error {
	in.MenuName = strings.TrimSpace(in.MenuName)
	in.MenuType = strings.ToUpper(strings.TrimSpace(in.MenuType))
	if in.MenuType == "" {
		in.MenuType = "C"
	}
	if in.MenuName == "" {
		return errors.New("菜单名称不能为空")
	}
	switch in.MenuType {
	case "M", "C", "F":
	default:
		return ErrMenuTypeInvalid
	}
	if in.MenuType == "C" && strings.TrimSpace(in.Path) == "" {
		return errors.New("页面菜单必须填写访问路径")
	}
	if in.MenuType == "F" && strings.TrimSpace(in.Perms) == "" {
		return errors.New("按钮权限点必须填写权限标识")
	}
	return nil
}

// CreateMenu 新建菜单/权限点
func (s *RBACService) CreateMenu(in *MenuInput) (*model.Menu, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.ParentID > 0 {
		var cnt int64
		s.db.Model(&model.Menu{}).Where("id = ?", in.ParentID).Count(&cnt)
		if cnt == 0 {
			return nil, errors.New("上级菜单不存在")
		}
	}
	visible, status := 1, 1
	if in.Visible != nil {
		visible = *in.Visible
	}
	if in.Status != nil {
		status = *in.Status
	}
	m := &model.Menu{
		ParentID: in.ParentID, MenuName: in.MenuName, MenuType: in.MenuType,
		Path: strings.TrimSpace(in.Path), Perms: strings.TrimSpace(in.Perms),
		Icon: in.Icon, SortNo: in.SortNo, Visible: visible, Status: status,
	}
	if err := s.db.Create(m).Error; err != nil {
		return nil, err
	}
	s.Invalidate()
	return m, nil
}

// UpdateMenu 更新菜单
func (s *RBACService) UpdateMenu(id int64, in *MenuInput) error {
	var m model.Menu
	if err := s.db.First(&m, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMenuNotFound
		}
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	if in.ParentID == id {
		return ErrMenuParentSelf
	}
	if in.ParentID > 0 && s.isDescendant(in.ParentID, id) {
		return ErrMenuParentSelf
	}
	m.ParentID = in.ParentID
	m.MenuName = in.MenuName
	m.MenuType = in.MenuType
	m.Path = strings.TrimSpace(in.Path)
	m.Perms = strings.TrimSpace(in.Perms)
	m.Icon = in.Icon
	m.SortNo = in.SortNo
	if in.Visible != nil {
		m.Visible = *in.Visible
	}
	if in.Status != nil {
		m.Status = *in.Status
	}
	if err := s.db.Save(&m).Error; err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

// DeleteMenu 删除菜单（含下级保护），并清理角色授权
func (s *RBACService) DeleteMenu(id int64) error {
	var cnt int64
	s.db.Model(&model.Menu{}).Where("parent_id = ?", id).Count(&cnt)
	if cnt > 0 {
		return ErrMenuHasChildren
	}
	res := s.db.Delete(&model.Menu{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrMenuNotFound
	}
	s.db.Where("menu_id = ?", id).Delete(&model.RoleMenu{})
	s.Invalidate()
	return nil
}

// isDescendant 判断 candidate 是否是 ancestorID 的下级（用于阻止把父菜单挂到自己的子节点下）
func (s *RBACService) isDescendant(candidate, ancestorID int64) bool {
	cur := candidate
	for i := 0; i < 20 && cur > 0; i++ { // 层级上限保护，避免脏数据死循环
		var m model.Menu
		if err := s.db.Select("parent_id").First(&m, cur).Error; err != nil {
			return false
		}
		if m.ParentID == ancestorID {
			return true
		}
		cur = m.ParentID
	}
	return false
}

// ---------- 用户-角色 ----------

// SetUserRoles 全量替换用户角色，并同步 sys_user.role（取第一个角色，兼容历史字段）
func (s *RBACService) SetUserRoles(userID int64, roleKeys []string) error {
	cleaned := make([]string, 0, len(roleKeys))
	seen := map[string]struct{}{}
	for _, k := range roleKeys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		cleaned = append(cleaned, k)
	}
	for _, k := range cleaned {
		var cnt int64
		s.db.Model(&model.Role{}).Where("role_key = ?", k).Count(&cnt)
		if cnt == 0 {
			return fmt.Errorf("角色不存在: %s", k)
		}
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserRole{}).Error; err != nil {
			return err
		}
		for _, k := range cleaned {
			if err := tx.Create(&model.UserRole{UserID: userID, RoleKey: k}).Error; err != nil {
				return err
			}
		}
		primary := ""
		if len(cleaned) > 0 {
			primary = cleaned[0]
		}
		return tx.Model(&model.User{}).Where("id = ?", userID).Update("role", primary).Error
	})
	if err == nil {
		s.Invalidate()
	}
	return err
}

// UserRoleMap 批量取用户角色（用户列表回显）
func (s *RBACService) UserRoleMap() map[int64][]string {
	var rows []model.UserRole
	s.db.Order("id").Find(&rows)
	out := make(map[int64][]string, len(rows))
	for _, r := range rows {
		out[r.UserID] = append(out[r.UserID], r.RoleKey)
	}
	return out
}

// BackfillUserRoles 历史数据兼容：为没有角色关联的用户按 sys_user.role 补一条关联，
// 并确保这些历史角色标识在 sys_role 中存在，避免升级后老账号登录即失权。
func BackfillUserRoles(db *gorm.DB) error {
	var users []model.User
	if err := db.Select("id", "role").Find(&users).Error; err != nil {
		return err
	}
	for _, u := range users {
		var cnt int64
		db.Model(&model.UserRole{}).Where("user_id = ?", u.ID).Count(&cnt)
		if cnt > 0 {
			continue
		}
		key := strings.TrimSpace(u.Role)
		if key == "" {
			// sys_user.role 已被清空，说明是「有意不给角色」，不自动补
			continue
		}
		var roleCnt int64
		db.Model(&model.Role{}).Where("role_key = ?", key).Count(&roleCnt)
		if roleCnt == 0 {
			name := "历史角色-" + key
			if key == "admin" {
				name = "超级管理员"
			} else if key == "user" {
				name = "普通用户"
			}
			if err := db.Create(&model.Role{RoleKey: key, RoleName: name, SortNo: 99, Status: 1, Builtin: 1, Remark: "升级时按 sys_user.role 自动补齐"}).Error; err != nil {
				return err
			}
		}
		if err := db.Create(&model.UserRole{UserID: u.ID, RoleKey: key}).Error; err != nil {
			return err
		}
	}
	return nil
}
