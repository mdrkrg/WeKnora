package lti

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Audit actions emitted by the protocol handlers. Defined package-locally
// (following the lti.member_provisioned precedent) so the shell stays
// deployment-agnostic and does not extend the platform's types package.
const (
	// AuditActionLTITicketIssued fires when a launch resolves an account and
	// a single-use ticket is minted.
	AuditActionLTITicketIssued types.AuditAction = "lti.ticket_issued"
	// AuditActionLTITicketRedeemed fires when a ticket is successfully
	// exchanged for a session JWT pair.
	AuditActionLTITicketRedeemed types.AuditAction = "lti.ticket_redeemed"
	// AuditActionLTITicketRedeemDenied fires when redemption is rejected,
	// most notably a replay attempt (reason=consumed).
	AuditActionLTITicketRedeemDenied types.AuditAction = "lti.ticket_redeem_denied"
)

// Handler serves the LTI endpoints. All dependencies are injected so the
// package stays deployment-agnostic.
type Handler struct {
	cfg           *config.LTIConfig
	registrations RegistrationStore
	tickets       TicketService
	keys          ToolKeyStore
	verifier      *Verifier
	resolver      IdentityResolver
	minter        TokenMinter
	audit         AuditSink
}

// NewHandler wires an LTI handler.
func NewHandler(
	cfg *config.LTIConfig,
	registrations RegistrationStore,
	tickets TicketService,
	keys ToolKeyStore,
	verifier *Verifier,
	resolver IdentityResolver,
	minter TokenMinter,
	audit AuditSink,
) *Handler {
	return &Handler{
		cfg:           cfg,
		registrations: registrations,
		tickets:       tickets,
		keys:          keys,
		verifier:      verifier,
		resolver:      resolver,
		minter:        minter,
		audit:         audit,
	}
}

// emitAudit is a no-op when no audit sink is configured.
func (h *Handler) emitAudit(ctx context.Context, entry *types.AuditLog) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Log(ctx, entry)
}

// auditDetailsJSON renders a key/value map as the Details JSON payload.
func auditDetailsJSON(m map[string]any) types.JSON {
	b, err := json.Marshal(m)
	if err != nil {
		return types.JSON("{}")
	}
	return types.JSON(b)
}

// LoginInitiation implements the LTI 1.3 OIDC third-party initiated login
// endpoint (POST /lti/login_initiations): it resolves the platform
// registration and redirects the browser to the platform's authorization
// endpoint with a signed nonce state.
func (h *Handler) LoginInitiation(c *gin.Context) {
	if !h.enabled() {
		h.renderFailure(c, http.StatusNotFound, "LTI 未启用", "LTI 登录通道尚未启用。")
		return
	}
	iss := strings.TrimSpace(c.PostForm("iss"))
	clientID := strings.TrimSpace(c.PostForm("client_id"))
	loginHint := strings.TrimSpace(c.PostForm("login_hint"))
	targetLink := strings.TrimSpace(c.PostForm("target_link_uri"))
	messageHint := strings.TrimSpace(c.PostForm("lti_message_hint"))
	if iss == "" || clientID == "" {
		h.renderFailure(c, http.StatusBadRequest, "参数缺失", "缺少 iss 或 client_id。")
		return
	}
	reg, err := h.registrations.GetByIssuerAndClientID(c.Request.Context(), iss, clientID)
	if err != nil {
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "查询 LTI 注册信息失败。")
		return
	}
	if reg == nil || !reg.Enabled {
		h.renderFailure(c, http.StatusNotFound, "未注册的 LTI 平台", "该平台（iss/client_id）未配置 LTI 注册。")
		return
	}
	if reg.AuthEndpoint == "" {
		h.renderFailure(c, http.StatusInternalServerError, "配置错误", "该注册缺少授权端点。")
		return
	}
	nonce, err := randomToken()
	if err != nil {
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "生成 nonce 失败。")
		return
	}
	state, err := SignNonceState(nonce)
	if err != nil {
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "生成 state 失败。")
		return
	}

	q := url.Values{}
	q.Set("response_type", "id_token")
	q.Set("scope", "openid")
	q.Set("response_mode", "form_post")
	q.Set("client_id", reg.ClientID)
	q.Set("redirect_uri", h.launchURL(c))
	q.Set("nonce", nonce)
	q.Set("state", state)
	q.Set("prompt", "none")
	if loginHint != "" {
		q.Set("login_hint", loginHint)
	}
	if targetLink != "" {
		q.Set("target_link_uri", targetLink)
	}
	if messageHint != "" {
		q.Set("lti_message_hint", messageHint)
	}

	var location string
	if u, perr := url.Parse(reg.AuthEndpoint); perr == nil {
		merged := u.Query()
		for k, vs := range q {
			for _, v := range vs {
				merged.Add(k, v)
			}
		}
		u.RawQuery = merged.Encode()
		location = u.String()
	} else {
		location = reg.AuthEndpoint + "?" + q.Encode()
	}
	c.Redirect(http.StatusFound, location)
}

// Launch consumes the platform's form_post id_token, resolves the account and
// issues a single-use ticket (POST /lti/launch).
func (h *Handler) Launch(c *gin.Context) {
	if !h.enabled() {
		h.renderFailure(c, http.StatusNotFound, "LTI 未启用", "LTI 登录通道尚未启用。")
		return
	}
	rawToken := strings.TrimSpace(c.PostForm("id_token"))
	stateRaw := strings.TrimSpace(c.PostForm("state"))
	if rawToken == "" || stateRaw == "" {
		h.renderFailure(c, http.StatusBadRequest, "参数缺失", "缺少 id_token 或 state。")
		return
	}
	state, err := VerifyNonceState(stateRaw, h.cfg.NonceMaxAge)
	if err != nil {
		h.renderFailure(c, http.StatusBadRequest, "无效请求", "state 校验失败，请从课程入口重新进入。")
		return
	}

	// The registration is selected from the unverified token claims; the
	// signature is only trusted after Verify against that registration.
	claims, err := parseUnverifiedClaims(rawToken)
	if err != nil {
		h.renderFailure(c, http.StatusBadRequest, "无效请求", "无法解析 id_token。")
		return
	}
	iss, _ := claims["iss"].(string)
	aud := firstAudience(claims["aud"])
	if iss == "" || aud == "" {
		h.renderFailure(c, http.StatusBadRequest, "无效请求", "id_token 缺少 iss 或 aud。")
		return
	}
	reg, err := h.registrations.GetByIssuerAndClientID(c.Request.Context(), iss, aud)
	if err != nil {
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "查询 LTI 注册信息失败。")
		return
	}
	if reg == nil || !reg.Enabled {
		// Untrusted token against an unknown registration.
		c.Status(http.StatusUnauthorized)
		return
	}

	vt, err := h.verifier.Verify(c.Request.Context(), rawToken, reg)
	if err != nil {
		h.renderFailure(c, http.StatusBadRequest, "身份校验失败", "id_token 签名或声明校验未通过。")
		return
	}
	if state.Nonce != "" && vt.Nonce != state.Nonce {
		h.renderFailure(c, http.StatusBadRequest, "身份校验失败", "nonce 不匹配。")
		return
	}

	// Record the deployment seen at launch so operators with an empty
	// deployment_ids allowlist (which deliberately admits any deployment) can
	// detect which deployment actually launched.
	logger.Infof(c.Request.Context(), "[LTI] launch verified: iss=%s client_id=%s deployment_id=%s sub=%s",
		reg.Issuer, reg.ClientID, vt.DeploymentID, vt.Sub)

	res, err := h.resolver.Resolve(c.Request.Context(), &LaunchIdentity{
		RegistrationID: reg.ID,
		Sub:            vt.Sub,
		Email:          vt.Email,
		DirectoryUID:   vt.DirectoryUID,
		Roles:          vt.Roles,
	})
	if err != nil {
		if errors.Is(err, ErrIdentityDisabled) {
			h.renderFailure(c, http.StatusBadRequest, "暂不可用", "LTI 身份解析尚未配置。")
			return
		}
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "身份解析失败。")
		return
	}

	raw, err := h.tickets.Issue(c.Request.Context(), res.UserID, vt.ContextID, vt.Roles)
	if err != nil {
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "签发登录凭证失败。")
		return
	}
	h.emitAudit(c.Request.Context(), &types.AuditLog{
		Action:        AuditActionLTITicketIssued,
		ActorUserID:   res.UserID,
		TargetType:    "lti_ticket",
		RequestPath:   c.Request.URL.Path,
		RequestMethod: c.Request.Method,
		Outcome:       types.AuditOutcomeSuccess,
		Details: auditDetailsJSON(map[string]any{
			"iss":        reg.Issuer,
			"client_id":  reg.ClientID,
			"context_id": vt.ContextID,
		}),
	})
	handoff := strings.TrimSpace(h.cfg.HandoffURL)
	if handoff == "" {
		h.renderFailure(c, http.StatusInternalServerError, "配置错误", "未配置 handoff 地址。")
		return
	}
	c.Redirect(http.StatusFound, handoff+"?ticket="+url.QueryEscape(raw))
}

// JWKS exposes the tool's public signing keys (GET /.well-known/jwks.json).
func (h *Handler) JWKS(c *gin.Context) {
	key, err := h.keys.Ensure(c.Request.Context())
	if err != nil {
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "生成工具密钥失败。")
		return
	}
	var jwk json.RawMessage
	if err := json.Unmarshal([]byte(key.PublicJWK), &jwk); err != nil {
		h.renderFailure(c, http.StatusInternalServerError, "服务错误", "工具密钥数据损坏。")
		return
	}
	c.JSON(http.StatusOK, gin.H{"keys": []json.RawMessage{jwk}})
}

// Redeem exchanges a single-use ticket for a session JWT pair, optionally
// targeted at a specific tenant (POST /lti/tickets/redeem).
func (h *Handler) Redeem(c *gin.Context) {
	if !h.validSharedSecret(c.GetHeader("Authorization")) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req struct {
		Ticket   string  `json:"ticket"`
		TenantID *uint64 `json:"tenant_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Ticket == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	ticket, err := h.tickets.Consume(c.Request.Context(), req.Ticket)
	if err != nil {
		reason := "expired_or_unknown"
		switch {
		case errors.Is(err, ErrTicketConsumed):
			reason = "consumed"
			c.JSON(http.StatusConflict, gin.H{"error": "ticket already used"})
		case errors.Is(err, ErrTicketExpired), errors.Is(err, ErrTicketNotFound):
			c.JSON(http.StatusGone, gin.H{"error": "ticket invalid or expired"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
			return
		}
		h.emitAudit(c.Request.Context(), &types.AuditLog{
			Action:        AuditActionLTITicketRedeemDenied,
			RequestPath:   c.Request.URL.Path,
			RequestMethod: c.Request.Method,
			Outcome:       types.AuditOutcomeDenied,
			Details: auditDetailsJSON(map[string]any{
				"reason": reason,
			}),
		})
		return
	}

	var tokens *TokenResult
	if req.TenantID != nil && *req.TenantID > 0 {
		tokens, err = h.minter.IssueForTenant(c.Request.Context(), ticket.UserID, *req.TenantID)
		if errors.Is(err, ErrNotTenantMember) {
			c.JSON(http.StatusForbidden, gin.H{"error": "not a member of the target workspace"})
			return
		}
	} else {
		tokens, err = h.minter.IssueDefault(c.Request.Context(), ticket.UserID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}

	details := map[string]any{"context_id": ticket.ContextID}
	if req.TenantID != nil {
		details["tenant_id"] = *req.TenantID
	}
	h.emitAudit(c.Request.Context(), &types.AuditLog{
		Action:        AuditActionLTITicketRedeemed,
		ActorUserID:   ticket.UserID,
		TargetType:    "lti_ticket",
		RequestPath:   c.Request.URL.Path,
		RequestMethod: c.Request.Method,
		Outcome:       types.AuditOutcomeSuccess,
		Details:       auditDetailsJSON(details),
	})

	c.JSON(http.StatusOK, gin.H{
		"user_id":       ticket.UserID,
		"context_id":    ticket.ContextID,
		"roles":         ticket.Roles,
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
	})
}

func (h *Handler) enabled() bool {
	return h.cfg != nil && h.cfg.Enable
}

// launchURL returns the tool's own launch endpoint, from config when set and
// derived from the request otherwise. When TLS terminates at a reverse proxy,
// the backend only ever sees plain HTTP; the X-Forwarded-Proto header (which
// nginx forwards from $scheme) is honored so the OIDC redirect_uri matches the
// registered https launch URL.
func (h *Handler) launchURL(c *gin.Context) string {
	if h.cfg != nil && strings.TrimSpace(h.cfg.LaunchURL) != "" {
		return strings.TrimRight(strings.TrimSpace(h.cfg.LaunchURL), "/")
	}
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
		if proto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); proto != "" {
			scheme = strings.ToLower(strings.TrimSpace(strings.Split(proto, ",")[0]))
		}
	}
	return scheme + "://" + c.Request.Host + "/lti/launch"
}

// validSharedSecret does a constant-time comparison of the Bearer header
// against the configured handoff shared secret.
func (h *Handler) validSharedSecret(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	cfg := ""
	if h.cfg != nil {
		cfg = h.cfg.HandoffSharedSecret
	}
	if cfg == "" || token == "" || len(cfg) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cfg), []byte(token)) == 1
}

func (h *Handler) renderFailure(c *gin.Context, status int, title, detail string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	_, _ = c.Writer.WriteString(failPageHTML(html.EscapeString(title), html.EscapeString(detail)))
}

func failPageHTML(title, detail string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>LTI 登录</title></head>
<body style="font-family:system-ui,sans-serif;text-align:center;padding:64px 16px">
<h1>%s</h1><p>%s</p></body></html>`, title, detail)
}

// parseUnverifiedClaims extracts claims without verifying the signature; used
// only to select the registration to verify against.
func parseUnverifiedClaims(raw string) (jwt.MapClaims, error) {
	token, _, err := new(jwt.Parser).ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("lti: unexpected claims type")
	}
	return claims, nil
}
