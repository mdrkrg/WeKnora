package lti

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// BindingsHandler serves the daemon's directory-binding push API. It derives
// the canonical authority server-side from the referenced registration, so the
// daemon and launch always agree on the binding key.
type BindingsHandler struct {
	registrations RegistrationStore
	identities    IdentityStore
}

// NewBindingsHandler wires the bindings push handler.
func NewBindingsHandler(registrations RegistrationStore, identities IdentityStore) *BindingsHandler {
	return &BindingsHandler{registrations: registrations, identities: identities}
}

// Create handles POST /api/v1/lti/bindings.
func (h *BindingsHandler) Create(c *gin.Context) {
	var req struct {
		RegistrationID uint64 `json:"registration_id"`
		ExternalUID    string `json:"external_uid"`
		UserID         string `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil ||
		req.RegistrationID == 0 || req.ExternalUID == "" || req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	reg, err := h.registrations.GetByID(c.Request.Context(), req.RegistrationID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}
	if reg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown registration"})
		return
	}

	now := time.Now()
	identity := &ExternalIdentity{
		UserID:      req.UserID,
		Authority:   sisAuthority(reg),
		ExternalUID: req.ExternalUID,
		ResolvedVia: "sis",
		CreatedAt:   now,
		LastSeenAt:  now,
	}
	if err := h.identities.Upsert(c.Request.Context(), identity); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"authority":    identity.Authority,
		"external_uid": identity.ExternalUID,
		"user_id":      identity.UserID,
		"resolved_via": identity.ResolvedVia,
	})
}
