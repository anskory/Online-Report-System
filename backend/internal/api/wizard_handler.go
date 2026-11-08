package api

import (
	"context"
	"net/http"
	"time"

	"github.com/ce/report-system/backend/internal/pkg/resp"
	"github.com/ce/report-system/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// WizardHandler 基于表生成报表草稿（只生成，不落库）
type WizardHandler struct {
	svc *service.WizardService
}

func NewWizardHandler(svc *service.WizardService) *WizardHandler {
	return &WizardHandler{svc: svc}
}

// Generate POST /api/report-wizard/generate
// 路径放在 /report-wizard 而不是 /reports/wizard，避免与 /reports/:id/... 的
// 通配段在同一层级冲突（gin 的静态段与参数段不能共存于同一位置）。
func (h *WizardHandler) Generate(c *gin.Context) {
	var req service.WizardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Err(c, http.StatusBadRequest, 2001, "参数错误: "+err.Error())
		return
	}
	// 首次会触发元数据扫描（后续命中缓存），给 30 秒余量
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	out, err := h.svc.Generate(ctx, req)
	if err != nil {
		resp.Err(c, http.StatusBadRequest, 2002, err.Error())
		return
	}
	resp.OK(c, out)
}
