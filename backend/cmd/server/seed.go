package main

import (
	"strings"

	"github.com/ce/report-system/backend/internal/model"
	"github.com/ce/report-system/backend/internal/service"
	"gorm.io/gorm"
)

// seedRBAC 初始化角色与菜单种子，并回填历史用户的角色关联。
// 幂等：角色按标识存在即跳过；菜单与默认授权只在整表为空时写入一次，之后完全由管理端接管。
func seedRBAC(db *gorm.DB) error {
	roles := []model.Role{
		{RoleKey: "admin", RoleName: "超级管理员", SortNo: 1, Status: 1, Builtin: 1,
			Remark: "拥有全部权限（隐式通配 *），不受菜单授权限制"},
		{RoleKey: "user", RoleName: "普通用户", SortNo: 2, Status: 1, Builtin: 1,
			Remark: "仅查看已授权且已发布的报表"},
	}
	for i := range roles {
		var cnt int64
		if err := db.Model(&model.Role{}).Where("role_key = ?", roles[i].RoleKey).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt == 0 {
			if err := db.Create(&roles[i]).Error; err != nil {
				return err
			}
		}
	}

	var menuCount int64
	if err := db.Model(&model.Menu{}).Count(&menuCount).Error; err != nil {
		return err
	}
	if menuCount == 0 {
		menus := defaultMenus()
		if err := db.Create(&menus).Error; err != nil {
			return err
		}
		// 普通用户默认只给「报表中心」入口；后续调整由角色管理页接管，不再自动补
		if err := db.Create(&model.RoleMenu{RoleKey: "user", MenuID: reportViewMenuID}).Error; err != nil {
			return err
		}
	}

	// 历史库里的菜单路径还是 /ui/xxx.html，统一改成干净地址（幂等）
	if err := normalizeMenuPaths(db); err != nil {
		return err
	}

	return service.BackfillUserRoles(db)
}

// normalizeMenuPaths 把历史菜单路径 /ui/<name>.html 改写为 /<name>。
// 前端已取消 /ui 前缀与 .html 后缀，若不迁移，导航高亮（按 location.pathname
// 与菜单 path 比较）会失配，且每次点击都要多走一次 301。
func normalizeMenuPaths(db *gorm.DB) error {
	var menus []model.Menu
	if err := db.Where("path LIKE ?", "/ui/%.html").Find(&menus).Error; err != nil {
		return err
	}
	for i := range menus {
		name := strings.TrimSuffix(strings.TrimPrefix(menus[i].Path, "/ui/"), ".html")
		if name == "" {
			continue
		}
		if err := db.Model(&model.Menu{}).Where("id = ?", menus[i].ID).
			Update("path", "/"+name).Error; err != nil {
			return err
		}
	}
	return nil
}

// reportViewMenuID 报表中心菜单的固定 ID（种子数据用固定 ID，便于授权关系引用）
const reportViewMenuID int64 = 1

// defaultMenus 默认菜单与权限点。
// ID 显式指定：升级前菜单表为空，固定 ID 便于幂等地建立角色授权关系。
func defaultMenus() []model.Menu {
	page := func(id, parent int64, name, path, perms string, sort int, icon string) model.Menu {
		return model.Menu{ID: id, ParentID: parent, MenuName: name, MenuType: "C",
			Path: path, Perms: perms, Icon: icon, SortNo: sort, Visible: 1, Status: 1}
	}
	button := func(id, parent int64, name, perms string, sort int) model.Menu {
		return model.Menu{ID: id, ParentID: parent, MenuName: name, MenuType: "F",
			Perms: perms, SortNo: sort, Visible: 1, Status: 1}
	}
	return []model.Menu{
		page(1, 0, "报表中心", "/reports", "report:view", 1, "Document"),
		page(2, 0, "报表配置", "/config", "report:update", 2, "EditPen"),
		button(21, 2, "新建报表", "report:create", 1),
		button(22, 2, "删除报表", "report:delete", 2),
		button(23, 2, "发布/停用", "report:publish", 3),
		button(24, 2, "公开访问", "report:public", 4),
		button(25, 2, "预览报表", "report:preview", 5),
		page(3, 0, "数据源管理", "/datasources", "datasource:list", 3, "Coin"),
		button(31, 3, "新建数据源", "datasource:create", 1),
		button(32, 3, "修改数据源", "datasource:update", 2),
		button(33, 3, "删除数据源", "datasource:delete", 3),
		button(34, 3, "连接测试", "datasource:test", 4),
		page(4, 0, "字典管理", "/dictionaries", "dict:list", 4, "Collection"),
		button(41, 4, "新建字典", "dict:create", 1),
		button(42, 4, "修改字典", "dict:update", 2),
		button(43, 4, "删除字典", "dict:delete", 3),
		{ID: 5, ParentID: 0, MenuName: "系统管理", MenuType: "M", Icon: "Setting", SortNo: 9, Visible: 1, Status: 1},
		page(51, 5, "用户管理", "/users", "system:user:list", 1, "User"),
		button(511, 51, "新建用户", "system:user:create", 1),
		button(512, 51, "修改用户", "system:user:update", 2),
		button(513, 51, "删除用户", "system:user:delete", 3),
		button(514, 51, "重置密码", "system:user:resetPwd", 4),
		button(515, 51, "分配角色", "system:user:assignRole", 5),
		page(52, 5, "角色管理", "/roles", "system:role:list", 2, "Avatar"),
		button(521, 52, "新建角色", "system:role:create", 1),
		button(522, 52, "修改角色", "system:role:update", 2),
		button(523, 52, "删除角色", "system:role:delete", 3),
		button(524, 52, "分配菜单", "system:role:assignMenu", 4),
		button(525, 52, "分配报表", "system:role:assignReport", 5),
		page(53, 5, "菜单管理", "/menus", "system:menu:list", 3, "Menu"),
		button(531, 53, "新建菜单", "system:menu:create", 1),
		button(532, 53, "修改菜单", "system:menu:update", 2),
		button(533, 53, "删除菜单", "system:menu:delete", 3),
	}
}
