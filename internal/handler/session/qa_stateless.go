package session

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// KnowledgeQAStateless handles stateless knowledge QA requests.
// POST /api/v1/knowledge-chat-stateless (spec section 1-4)
func (h *Handler) KnowledgeQAStateless(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"success": false, "error": "not implemented"})
}

// StopStatelessQA handles stop requests for stateless QA.
// POST /api/v1/knowledge-chat-stateless/stop (spec section 6)
func (h *Handler) StopStatelessQA(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"success": false, "error": "not implemented"})
}
