package lti

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/gin-gonic/gin"
)

// FrameAncestorsMiddleware sets a frame-ancestors CSP header for LTI pages so
// they can be embedded in the platform's iframe while everything else stays
// untouched. The allowed origins come from LTI_FRAME_ANCESTORS and default to
// 'self'.
func FrameAncestorsMiddleware(cfg *config.LTIConfig) gin.HandlerFunc {
	sources := "'self'"
	if cfg != nil && strings.TrimSpace(cfg.FrameAncestors) != "" {
		sources = strings.TrimSpace(cfg.FrameAncestors)
	}
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/lti/") {
			c.Header("Content-Security-Policy", "frame-ancestors "+sources)
		}
		c.Next()
	}
}
