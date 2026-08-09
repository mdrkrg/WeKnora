package handler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource/oauth"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// DataSourceOAuthHandler exposes the data-source OAuth2 authorization-code flow.
type DataSourceOAuthHandler struct {
	oauth     *oauth.Manager
	dsService interfaces.DataSourceService
	kbService interfaces.KnowledgeBaseService
}

// NewDataSourceOAuthHandler constructs the handler.
func NewDataSourceOAuthHandler(
	oauthMgr *oauth.Manager,
	dsService interfaces.DataSourceService,
	kbService interfaces.KnowledgeBaseService,
) *DataSourceOAuthHandler {
	return &DataSourceOAuthHandler{oauth: oauthMgr, dsService: dsService, kbService: kbService}
}

type dsOAuthAuthorizeRequest struct {
	RedirectURI      string `json:"redirect_uri"`
	FrontendRedirect string `json:"frontend_redirect"`
}

func (h *DataSourceOAuthHandler) ownDataSource(c *gin.Context) (*types.DataSource, bool) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewUnauthorizedError("authentication required"))
		return nil, false
	}
	id := c.Param("id")
	ds, err := h.dsService.GetDataSource(c.Request.Context(), id)
	if err != nil || ds == nil {
		c.Error(errors.NewNotFoundError("data source not found"))
		return nil, false
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(c.Request.Context(), ds.KnowledgeBaseID)
	if err != nil || kb == nil || kb.TenantID != tenantID {
		c.Error(errors.NewForbiddenError("access denied"))
		return nil, false
	}
	return ds, true
}

// AuthorizeURL begins authorization and returns the URL the browser must open.
func (h *DataSourceOAuthHandler) AuthorizeURL(c *gin.Context) {
	ctx := c.Request.Context()
	ds, ok := h.ownDataSource(c)
	if !ok {
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req dsOAuthAuthorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	req.RedirectURI = strings.TrimSpace(req.RedirectURI)
	if req.RedirectURI == "" {
		c.Error(errors.NewValidationError("redirect_uri is required"))
		return
	}
	if req.FrontendRedirect == "" {
		req.FrontendRedirect = "/"
	}

	authURL, err := h.oauth.StartAuthorization(ctx, ds, tenantID, req.RedirectURI, req.FrontendRedirect)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"data_source_id": ds.ID})
		c.Error(errors.NewInternalServerError("failed to start authorization: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"authorization_url": authURL}})
}

// Callback receives the authorization-server redirect (public, state-authenticated).
func (h *DataSourceOAuthHandler) Callback(c *gin.Context) {
	ctx := c.Request.Context()
	state := strings.TrimSpace(c.Query("state"))
	code := strings.TrimSpace(c.Query("code"))
	providerErr := strings.TrimSpace(c.Query("error"))

	const fallbackRedirect = "/"

	if providerErr != "" {
		c.Redirect(http.StatusFound, fallbackRedirect+"#ds_oauth_error="+url.QueryEscape(providerErr))
		return
	}
	if state == "" || code == "" {
		c.Redirect(http.StatusFound, fallbackRedirect+"#ds_oauth_error="+url.QueryEscape("missing_code_or_state"))
		return
	}

	frontendRedirect, err := h.oauth.CompleteAuthorization(ctx, state, code)
	if frontendRedirect == "" {
		frontendRedirect = fallbackRedirect
	}
	if err != nil {
		logger.Errorf(ctx, "data source OAuth callback failed: %v", err)
		c.Redirect(http.StatusFound, frontendRedirect+"#ds_oauth_error="+url.QueryEscape("authorization_failed"))
		return
	}
	c.Redirect(http.StatusFound, frontendRedirect+"#ds_oauth_result=success")
}

// Status reports whether the data source has completed OAuth authorization.
func (h *DataSourceOAuthHandler) Status(c *gin.Context) {
	ctx := c.Request.Context()
	ds, ok := h.ownDataSource(c)
	if !ok {
		return
	}
	authorized, err := h.oauth.IsAuthorized(ctx, ds)
	if err != nil {
		c.Error(errors.NewInternalServerError("failed to query authorization status: " + err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"authorized": authorized}})
}

// Revoke clears stored OAuth tokens for the data source.
func (h *DataSourceOAuthHandler) Revoke(c *gin.Context) {
	ctx := c.Request.Context()
	ds, ok := h.ownDataSource(c)
	if !ok {
		return
	}
	if err := h.oauth.Revoke(ctx, ds); err != nil {
		c.Error(errors.NewInternalServerError("failed to revoke authorization: " + err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}
