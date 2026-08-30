package lti

import (
	"context"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// Audit actions emitted by the bindings API. Defined package-locally (like
// the ticket actions) so the shell stays deployment-agnostic and does not
// extend the platform's types package.
const (
	// AuditActionLTIBindingPushed fires when a directory-binding push is
	// accepted (create or idempotent overwrite).
	AuditActionLTIBindingPushed types.AuditAction = "lti.binding_pushed"
	// AuditActionLTIBindingDeleted fires when a binding row is removed via
	// the DELETE endpoint.
	AuditActionLTIBindingDeleted types.AuditAction = "lti.binding_deleted"
)

// BindingsHandler serves the daemon's directory-binding push API. It derives
// the canonical authority server-side from the referenced registration, so the
// daemon and launch always agree on the binding key.
type BindingsHandler struct {
	registrations RegistrationStore
	identities    IdentityStore
	catalog       UserCatalog
	audit         AuditSink
}

// NewBindingsHandler wires the bindings push/delete handler.
func NewBindingsHandler(
	registrations RegistrationStore,
	identities IdentityStore,
	catalog UserCatalog,
	audit AuditSink,
) *BindingsHandler {
	return &BindingsHandler{
		registrations: registrations,
		identities:    identities,
		catalog:       catalog,
		audit:         audit,
	}
}

// emitAudit records an audit event through the injected sink; a nil sink
// degrades to a no-op, mirroring Handler.emitAudit.
func (h *BindingsHandler) emitAudit(ctx context.Context, entry *types.AuditLog) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Log(ctx, entry)
}

// launchAuthority is the namespaced authority for a launch-sub binding,
// mirroring the matcher's step-1 lookup key.
func launchAuthority(reg *Registration) string {
	return "lti:" + reg.ClientID
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
	h.emitAudit(c.Request.Context(), &types.AuditLog{
		Action:        AuditActionLTIBindingPushed,
		ActorUserID:   identity.UserID,
		TargetType:    "lti_binding",
		RequestPath:   c.Request.URL.Path,
		RequestMethod: c.Request.Method,
		Outcome:       types.AuditOutcomeSuccess,
		Details: auditDetailsJSON(map[string]any{
			"authority":    identity.Authority,
			"external_uid": identity.ExternalUID,
			"user_id":      identity.UserID,
			"resolved_via": identity.ResolvedVia,
		}),
	})
	c.JSON(http.StatusOK, gin.H{
		"authority":    identity.Authority,
		"external_uid": identity.ExternalUID,
		"user_id":      identity.UserID,
		"resolved_via": identity.ResolvedVia,
	})
}

// Delete handles DELETE /api/v1/lti/bindings: removes a binding row by
// (registration-derived authority, external uid). scope selects the binding
// namespace: "directory" (the default) targets the sis:{iss} row, "launch"
// the lti:{client_id} row, which has no other management surface. Deleting
// zero rows answers 404 so callers can tell a no-op from success (204).
func (h *BindingsHandler) Delete(c *gin.Context) {
	var req struct {
		RegistrationID uint64 `json:"registration_id"`
		ExternalUID    string `json:"external_uid"`
		Scope          string `json:"scope"`
	}
	if err := c.ShouldBindJSON(&req); err != nil ||
		req.RegistrationID == 0 || req.ExternalUID == "" {
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

	scope := req.Scope
	if scope == "" {
		scope = "directory"
	}
	authority := ""
	switch scope {
	case "directory":
		authority = sisAuthority(reg)
	case "launch":
		authority = launchAuthority(reg)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid scope"})
		return
	}

	deleted, err := h.identities.Delete(c.Request.Context(), authority, req.ExternalUID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}
	if deleted == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "binding not found"})
		return
	}
	h.emitAudit(c.Request.Context(), &types.AuditLog{
		Action:        AuditActionLTIBindingDeleted,
		TargetType:    "lti_binding",
		RequestPath:   c.Request.URL.Path,
		RequestMethod: c.Request.Method,
		Outcome:       types.AuditOutcomeSuccess,
		Details: auditDetailsJSON(map[string]any{
			"authority":    authority,
			"external_uid": req.ExternalUID,
			"scope":        scope,
		}),
	})
	c.Status(http.StatusNoContent)
}
