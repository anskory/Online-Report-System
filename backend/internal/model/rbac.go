package model

import "time"

// Role 角色（表 sys_role）。
// RoleKey 是稳定标识（如 admin/report_dev），业务代码与权限关联一律用它，不随显示名变化。
type Role struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	RoleKey   string    `gorm:"size:32;uniqueIndex;not null" json:"roleKey"`
	RoleName  string    `gorm:"size:64;not null" json:"roleName"`
	SortNo    int       `gorm:"default:0" json:"sortNo"`
	Status    int       `gorm:"default:1" json:"status"`  // 1启用 0停用（停用后其权限不再生效）
	Builtin   int       `gorm:"default:0" json:"builtin"` // 1=内置角色：不可删除、不可改标识
	Remark    string    `gorm:"size:255;default:''" json:"remark"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (Role) TableName() string { return "sys_role" }

// Menu 菜单与权限点（表 sys_menu）。
// MenuType：M=目录（导航分组，无页面）、C=页面（对应一个前端路由，如 /reports）、F=按钮权限点（不出现在导航）。
// Perms 为该节点声明的权限标识，可逗号分隔多个；接口层 RequirePerm 与前端 can() 都按此校验。
type Menu struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	ParentID  int64     `gorm:"default:0;index" json:"parentId"`
	MenuName  string    `gorm:"size:64;not null" json:"menuName"`
	MenuType  string    `gorm:"size:2;default:'C'" json:"menuType"`
	Path      string    `gorm:"size:128;default:''" json:"path"`
	Perms     string    `gorm:"size:128;default:''" json:"perms"`
	Icon      string    `gorm:"size:64;default:''" json:"icon"`
	SortNo    int       `gorm:"default:0" json:"sortNo"`
	Visible   int       `gorm:"default:1" json:"visible"` // 1显示 0隐藏（隐藏但仍可作为权限点）
	Status    int       `gorm:"default:1" json:"status"`  // 1启用 0停用（停用后其权限不再生效）
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (Menu) TableName() string { return "sys_menu" }

// UserRole 用户-角色关联（表 sys_user_role）。一个用户可挂多个角色，权限取并集。
type UserRole struct {
	ID      int64  `gorm:"primaryKey" json:"id"`
	UserID  int64  `gorm:"not null;uniqueIndex:uk_user_role" json:"userId"`
	RoleKey string `gorm:"size:32;not null;uniqueIndex:uk_user_role" json:"roleKey"`
}

func (UserRole) TableName() string { return "sys_user_role" }

// RoleMenu 角色-菜单关联（表 sys_role_menu）。目录被授权后其子页面仍需单独勾选。
type RoleMenu struct {
	ID      int64  `gorm:"primaryKey" json:"id"`
	RoleKey string `gorm:"size:32;not null;uniqueIndex:uk_role_menu" json:"roleKey"`
	MenuID  int64  `gorm:"not null;uniqueIndex:uk_role_menu" json:"menuId"`
}

func (RoleMenu) TableName() string { return "sys_role_menu" }
