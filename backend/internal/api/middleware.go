// Package api 路由与中间件。
package api

import (
	"strings"

	"github.com/ce/report-system/backend/internal/pkg/jwtutil"
	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// 上下文中的当前用户信息 key
const (
	CtxUserID   = "uid"
	CtxUsername = "uname"
	CtxRoles    = "roles" // []string 角色标识集合
	CtxPerms    = "perms" // map[string]struct{} 权限点集合（含 * 表示全部）
)

// JWTAuth 登录鉴权中间件：校验 Bearer Token，并加载用户「当前」角色与权限点。
// 角色不作为令牌的信任源——每次请求都按库中的用户-角色关联解析，
// 这样管理端收回角色/停用角色后能立即生效，不必等令牌过期。
func JWTAuth(secret string, rbac *service.RBACService) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			resp.Err(c, 401, 1001, "未登录或令牌缺失")
			c.Abort()
			return
		}
		claims, err := jwtutil.Parse(secret, strings.TrimPrefix(h, "Bearer "))
		if err != nil || claims.Typ != "access" {
			resp.Err(c, 401, 1001, "令牌无效或已过期")
			c.Abort()
			return
		}
		roles := rbac.RolesOf(claims.UserID)
		if len(roles) == 0 {
			resp.Err(c, 403, 1003, "账号未分配角色或角色已停用，请联系管理员")
			c.Abort()
			return
		}
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxUsername, claims.Username)
		c.Set(CtxRoles, roles)
		c.Set(CtxPerms, rbac.PermsOf(roles))
		c.Next()
	}
}

// RequirePerm 权限点校验中间件（需在 JWTAuth 之后）
func RequirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms := CurrentPerms(c)
		if _, all := perms["*"]; all {
			c.Next()
			return
		}
		if _, ok := perms[perm]; !ok {
			resp.Err(c, 403, 1003, "没有操作权限："+perm)
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireAnyPerm 满足任一权限点即放行
func RequireAnyPerm(candidates ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms := CurrentPerms(c)
		if _, all := perms["*"]; all {
			c.Next()
			return
		}
		for _, p := range candidates {
			if _, ok := perms[p]; ok {
				c.Next()
				return
			}
		}
		resp.Err(c, 403, 1003, "没有操作权限："+strings.Join(candidates, " / "))
		c.Abort()
	}
}

// CurrentRoles 当前用户角色标识
func CurrentRoles(c *gin.Context) []string {
	if v, ok := c.Get(CtxRoles); ok {
		if roles, ok := v.([]string); ok {
			return roles
		}
	}
	return nil
}

// CurrentPerms 当前用户权限点集合
func CurrentPerms(c *gin.Context) map[string]struct{} {
	if v, ok := c.Get(CtxPerms); ok {
		if perms, ok := v.(map[string]struct{}); ok {
			return perms
		}
	}
	return map[string]struct{}{}
}

// IsAdminCtx 当前用户是否超级管理员
func IsAdminCtx(c *gin.Context) bool {
	return service.IsAdmin(CurrentRoles(c))
}

// CurrentUser 从上下文取当前用户（ID、用户名）
func CurrentUser(c *gin.Context) (int64, string) {
	return c.GetInt64(CtxUserID), c.GetString(CtxUsername)
}
