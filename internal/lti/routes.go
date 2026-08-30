package lti

import "github.com/gin-gonic/gin"

// RegisterPublicRoutes mounts the public LTI endpoints. It is intended to be
// called before the global Auth middleware so the endpoints are reachable
// without touching the noAuthAPI whitelist.
func RegisterPublicRoutes(r *gin.Engine, h *Handler) {
	r.POST("/lti/login_initiations", h.LoginInitiation)
	r.POST("/lti/launch", h.Launch)
	r.GET("/.well-known/jwks.json", h.JWKS)
	r.POST("/lti/tickets/redeem", h.Redeem)
}
