// Package resp 统一响应包装：{"code":0,"msg":"ok","data":{...}}
package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// OK 成功响应
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": data})
}

// Err 错误响应，code 为业务错误码（见设计文档第 11 章）
func Err(c *gin.Context, httpCode, bizCode int, msg string) {
	c.JSON(httpCode, gin.H{"code": bizCode, "msg": msg})
}
