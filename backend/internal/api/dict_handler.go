package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ce/report-system/backend/internal/model"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// DictHandler 数据字典管理接口（管理员）
type DictHandler struct {
	dict  *service.DictService
	ds    *service.DatasourceService
	audit *service.AuditService
}

func NewDictHandler(dict *service.DictService, ds *service.DatasourceService, audit *service.AuditService) *DictHandler {
	return &DictHandler{dict: dict, ds: ds, audit: audit}
}

// List 字典清单
func (h *DictHandler) List(c *gin.Context) {
	list, err := h.dict.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": list})
}

// Save 新建/更新字典
func (h *DictHandler) Save(c *gin.Context) {
	var d model.Dict
	if err := c.ShouldBindJSON(&d); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "参数错误: " + err.Error()})
		return
	}
	// 编辑时取路径 ID
	if id := c.Param("id"); id != "" {
		v, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "ID 非法"})
			return
		}
		d.ID = v
	}
	isNew := d.ID == 0 // Save 会回填 ID，必须在保存前判断
	if err := h.dict.Save(&d); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	// 字典变更此前没有任何审计记录，属于明显的日志缺口，这里补上
	action := "修改"
	if isNew {
		action = "新建"
	}
	e := NewAuditEntry(c, "config_change", "dict")
	e.Target = d.Name
	e.Detail = fmt.Sprintf("%s字典 %s（%s）", action, d.Name, d.Code)
	h.audit.LogAsync(e)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": d})
}

// Delete 删除字典
func (h *DictHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "ID 非法"})
		return
	}
	// 先取名字：删除后日志只剩 ID，事后看不出删的是哪个字典
	name, code := "", ""
	if d, e := h.dict.GetByID(id); e == nil && d != nil {
		name, code = d.Name, d.Code
	}
	if err := h.dict.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	e := NewAuditEntry(c, "config_change", "dict")
	e.Target = name
	if name != "" {
		e.Detail = fmt.Sprintf("删除字典 %s（%s）", name, code)
	} else {
		e.Detail = fmt.Sprintf("删除字典 ID=%d", id)
	}
	h.audit.LogAsync(e)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok"})
}

// Types 字典可用「字典类型」清单（报表配置时按类型筛选用）
func (h *DictHandler) Types(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "ID 非法"})
		return
	}
	d, err := h.dict.GetByID(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	types, err := h.dict.Types(ctx, h.ds, d)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": types})
}

// Options 按字典编码 + 类型取选项清单（报表配置页「默认值」下拉用，复用报表选项的数据通道）
func (h *DictHandler) Options(c *gin.Context) {
	var in struct {
		Code string `json:"code" binding:"required"`
		Type string `json:"type"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "参数错误: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	rows, err := h.dict.Options(ctx, h.ds, in.Code, in.Type)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": rows})
}

// Test 试跑字典定义（按提交内容直接执行，支持未保存的配置；返回最多 5000 行）
func (h *DictHandler) Test(c *gin.Context) {
	var d model.Dict
	if err := c.ShouldBindJSON(&d); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": "参数错误: " + err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	rows, err := h.dict.Test(ctx, h.ds, &d)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": rows})
}
