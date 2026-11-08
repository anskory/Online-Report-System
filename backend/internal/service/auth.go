package service

import (
	"errors"
	"strings"
	"time"

	"github.com/ce/report-system/backend/internal/model"
	"github.com/ce/report-system/backend/internal/pkg/jwtutil"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// 登录相关业务错误（handler 层据此映射 HTTP 状态码与提示）
var (
	ErrBadCredential = errors.New("用户名或密码错误")
	ErrAccountLocked = errors.New("账号已锁定，请稍后再试")
	ErrAccountBanned = errors.New("账号已禁用")
	ErrUserExists    = errors.New("用户名已存在")
	ErrUserNotFound  = errors.New("用户不存在")
)

// AuthService 认证与用户管理服务
type AuthService struct {
	db          *gorm.DB
	jwtSecret   string
	maxFail     int
	lockMinutes int
	audit       *AuditService
	rbac        *RBACService
}

func NewAuthService(db *gorm.DB, jwtSecret string, maxFail, lockMinutes int, audit *AuditService, rbac *RBACService) *AuthService {
	if maxFail <= 0 {
		maxFail = 5
	}
	if lockMinutes <= 0 {
		lockMinutes = 10
	}
	return &AuthService{db: db, jwtSecret: jwtSecret, maxFail: maxFail, lockMinutes: lockMinutes, audit: audit, rbac: rbac}
}

// LoginResult 登录结果
type LoginResult struct {
	AccessToken  string      `json:"accessToken"`
	RefreshToken string      `json:"refreshToken"`
	User         *model.User `json:"user"`
	Roles        []string    `json:"roles"`
}

// Login 登录：密码校验、失败锁定、签发双令牌
func (s *AuthService) Login(username, password, clientIP string) (*LoginResult, error) {
	var user model.User
	err := s.db.Where("username = ?", username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.audit.LogAsync(AuditEntry{LogType: "login", Username: username, ClientIP: clientIP, Detail: "用户不存在", Success: false})
		return nil, ErrBadCredential
	}
	if err != nil {
		return nil, err
	}
	if user.Status != 1 {
		return nil, ErrAccountBanned
	}
	// 锁定检查
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return nil, ErrAccountLocked
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		// 累计失败次数，达到阈值锁定
		updates := map[string]any{"fail_count": user.FailCount + 1}
		if user.FailCount+1 >= s.maxFail {
			until := time.Now().Add(time.Duration(s.lockMinutes) * time.Minute)
			updates["locked_until"] = until
			updates["fail_count"] = 0
		}
		s.db.Model(&user).Updates(updates)
		s.audit.LogAsync(AuditEntry{LogType: "login", UserID: user.ID, Username: username, ClientIP: clientIP, Detail: "密码错误", Success: false})
		if user.FailCount+1 >= s.maxFail {
			return nil, ErrAccountLocked
		}
		return nil, ErrBadCredential
	}

	// 角色以关联表为准（多角色），JWT 里的 role 仅为「主角色」展示用
	roles := s.rbac.RolesOf(user.ID)
	if len(roles) == 0 {
		return nil, errors.New("账号未分配角色，请联系管理员")
	}
	user.Role = roles[0]

	// 登录成功：清零失败计数，更新最后登录时间
	now := time.Now()
	s.db.Model(&user).Updates(map[string]any{"fail_count": 0, "locked_until": nil, "last_login_at": now})

	access, err := jwtutil.Sign(s.jwtSecret, "access", user.ID, user.Username, user.Role)
	if err != nil {
		return nil, err
	}
	refresh, err := jwtutil.Sign(s.jwtSecret, "refresh", user.ID, user.Username, user.Role)
	if err != nil {
		return nil, err
	}
	s.audit.LogAsync(AuditEntry{LogType: "login", UserID: user.ID, Username: username, ClientIP: clientIP, Detail: "登录成功"})
	return &LoginResult{AccessToken: access, RefreshToken: refresh, User: &user, Roles: roles}, nil
}

// Refresh 用刷新令牌换取新的访问令牌
func (s *AuthService) Refresh(refreshToken string) (string, error) {
	claims, err := jwtutil.Parse(s.jwtSecret, refreshToken)
	if err != nil {
		return "", errors.New("刷新令牌无效或已过期")
	}
	if claims.Typ != "refresh" {
		return "", errors.New("令牌类型错误")
	}
	var user model.User
	if err := s.db.First(&user, claims.UserID).Error; err != nil {
		return "", errors.New("用户不存在或已删除")
	}
	if user.Status != 1 {
		return "", ErrAccountBanned
	}
	roles := s.rbac.RolesOf(user.ID)
	if len(roles) == 0 {
		return "", errors.New("账号未分配角色，请联系管理员")
	}
	return jwtutil.Sign(s.jwtSecret, "access", user.ID, user.Username, roles[0])
}

// GetUser 按 ID 取用户
func (s *AuthService) GetUser(id int64) (*model.User, error) {
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}

// UserView 用户列表视图（带角色名）
type UserView struct {
	ID          int64       `json:"id"`
	Username    string      `json:"username"`
	DisplayName string      `json:"displayName"`
	Status      int         `json:"status"`
	LastLoginAt *time.Time  `json:"lastLoginAt"`
	CreatedAt   time.Time   `json:"createdAt"`
	Roles       []RoleBrief `json:"roles"`
}

// ListUsers 用户列表（按用户名/显示名模糊搜索）
func (s *AuthService) ListUsers(keyword string) ([]UserView, error) {
	q := s.db.Model(&model.User{})
	if kw := strings.TrimSpace(keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("username LIKE ? OR display_name LIKE ?", like, like)
	}
	var users []model.User
	if err := q.Order("id").Find(&users).Error; err != nil {
		return nil, err
	}
	roleMap := s.rbac.UserRoleMap()
	nameMap := s.rbac.RoleNameMap()
	out := make([]UserView, 0, len(users))
	for _, u := range users {
		keys := roleMap[u.ID]
		if len(keys) == 0 && u.Role != "" { // 兼容未回填的历史数据
			keys = []string{u.Role}
		}
		roles := make([]RoleBrief, 0, len(keys))
		for _, k := range keys {
			name := nameMap[k]
			if name == "" {
				name = k
			}
			roles = append(roles, RoleBrief{RoleKey: k, RoleName: name})
		}
		out = append(out, UserView{
			ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Status: u.Status,
			LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt, Roles: roles,
		})
	}
	return out, nil
}

// CreateUser 创建用户（管理员），密码 bcrypt 存储，并绑定角色
func (s *AuthService) CreateUser(username, password, displayName string, roleKeys []string, status int) (*model.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || strings.TrimSpace(password) == "" {
		return nil, errors.New("用户名和密码不能为空")
	}
	if len(password) < 6 {
		return nil, errors.New("密码长度不少于 6 位")
	}
	var cnt int64
	s.db.Model(&model.User{}).Where("username = ?", username).Count(&cnt)
	if cnt > 0 {
		return nil, ErrUserExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	primary := ""
	if len(roleKeys) > 0 {
		primary = roleKeys[0]
	}
	u := &model.User{Username: username, PasswordHash: string(hash), DisplayName: displayName, Role: primary, Status: status}
	if err := s.db.Create(u).Error; err != nil {
		return nil, err
	}
	if err := s.rbac.SetUserRoles(u.ID, roleKeys); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUser 更新显示名与状态
func (s *AuthService) UpdateUser(id int64, displayName string, status int) error {
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		return err
	}
	return s.db.Model(&u).Updates(map[string]any{"display_name": displayName, "status": status}).Error
}

// ResetPassword 重置密码并解锁
func (s *AuthService) ResetPassword(id int64, password string) error {
	if len(password) < 6 {
		return errors.New("密码长度不少于 6 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res := s.db.Model(&model.User{}).Where("id = ?", id).Updates(map[string]any{
		"password_hash": string(hash), "fail_count": 0, "locked_until": nil,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteUser 删除用户并清理角色关联
func (s *AuthService) DeleteUser(id int64) error {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Delete(&model.User{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrUserNotFound
		}
		return tx.Where("user_id = ?", id).Delete(&model.UserRole{}).Error
	})
	return err
}

// IsLastAdmin 判断该用户是否为最后一个启用的管理员（删除/停用前的保护）
func (s *AuthService) IsLastAdmin(id int64) bool {
	var u model.User
	if err := s.db.First(&u, id).Error; err != nil {
		return false
	}
	var cnt int64
	s.db.Table("sys_user_role AS ur").
		Joins("JOIN sys_user AS u ON u.id = ur.user_id").
		Where("ur.role_key = 'admin' AND u.status = 1 AND u.id <> ?", id).Count(&cnt)
	if cnt > 0 {
		return false
	}
	// 关联表还没有 admin 记录（历史数据）时按 sys_user.role 判断
	var legacy int64
	s.db.Model(&model.User{}).Where("role = 'admin' AND status = 1 AND id <> ?", id).Count(&legacy)
	return legacy == 0
}
