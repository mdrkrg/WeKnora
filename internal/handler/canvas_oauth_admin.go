package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// CanvasOAuthAdminHandler manages workspace-level Canvas OAuth app credentials.
type CanvasOAuthAdminHandler struct {
	svc interfaces.CanvasOAuthService
}

// NewCanvasOAuthAdminHandler constructs the handler.
func NewCanvasOAuthAdminHandler(svc interfaces.CanvasOAuthService) *CanvasOAuthAdminHandler {
	return &CanvasOAuthAdminHandler{svc: svc}
}

// Status GET /api/v1/canvas/oauth/status
func (h *CanvasOAuthAdminHandler) Status(c *gin.Context) {
	result, err := h.svc.CheckStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
