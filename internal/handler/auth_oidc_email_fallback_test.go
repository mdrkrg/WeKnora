package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fallbackInviteUserService is a UserService stub for the register-by-
// invite handler: only the methods the handler touches are implemented,
// the rest panic via the embedded interface.
type fallbackInviteUserService struct {
	interfaces.UserService
	registerCalled bool
	registerEmail  string
}

func (s *fallbackInviteUserService) GetUserByEmail(context.Context, string) (*types.User, error) {
	return nil, nil
}

func (s *fallbackInviteUserService) Register(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
	s.registerCalled = true
	s.registerEmail = req.Email
	return &types.User{ID: "u1", Email: req.Email}, nil
}

func (s *fallbackInviteUserService) UpdateUser(context.Context, *types.User) error {
	return nil
}

func (s *fallbackInviteUserService) DeleteUser(context.Context, string) error {
	return nil
}

func (s *fallbackInviteUserService) GenerateTokens(context.Context, *types.User) (string, string, error) {
	return "access-token", "refresh-token", nil
}

// fallbackInvitationService accepts every share-link token.
type fallbackInvitationService struct {
	interfaces.TenantInvitationService
}

func (s *fallbackInvitationService) LookupByToken(context.Context, string) (*types.TenantInvitation, error) {
	return &types.TenantInvitation{
		TenantID:  1,
		Role:      types.TenantRoleContributor,
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

func (s *fallbackInvitationService) AcceptByToken(context.Context, string, string) (*types.TenantMember, error) {
	return &types.TenantMember{}, nil
}

// fallbackInviteTenantService returns no tenant, matching a tenant the
// invitee's registration has not created yet.
type fallbackInviteTenantService struct {
	interfaces.TenantService
}

func (s *fallbackInviteTenantService) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return nil, nil
}

func newRegisterByInviteRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/auth/register-by-invite", h.RegisterByInvite)
	return r
}

func doRegisterByInvite(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/register-by-invite", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func validInviteBody(email string) map[string]string {
	return map[string]string{
		"token":    "invite-token",
		"email":    email,
		"username": "alice",
		"password": "supersecret",
	}
}

func fallbackHandler(us interfaces.UserService, inv interfaces.TenantInvitationService) *AuthHandler {
	return NewAuthHandler(&config.Config{
		OIDCAuth: &config.OIDCAuthConfig{Enable: true, EmailFallbackDomain: "sjtu.edu.cn"},
		Auth:     &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, &fallbackInviteTenantService{}, nil, inv)
}

// TestResolveRegistrationMode_ForcedInviteOnlyWhenEmailFallbackEnabled
// pins the runtime enforcement: with email fallback active, the
// registration mode must be invite_only regardless of what cfg or the
// system settings say - a synthesized email is not provider-verified and
// open registration would allow pre-registering it.
func TestResolveRegistrationMode_ForcedInviteOnlyWhenEmailFallbackEnabled(t *testing.T) {
	h := NewAuthHandler(&config.Config{
		OIDCAuth: &config.OIDCAuthConfig{Enable: true, EmailFallbackDomain: "sjtu.edu.cn"},
		Auth:     &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeSelfServe},
	}, nil, nil, nil, nil)

	if got := h.resolveRegistrationMode(context.Background()); got != config.AuthRegistrationModeInviteOnly {
		t.Fatalf("registration mode = %q, want forced invite_only", got)
	}
}

// TestResolveRegistrationMode_UnaffectedWithoutEmailFallback verifies
// the force is scoped to the fallback mode: a plain OIDC setup keeps the
// configured registration mode.
func TestResolveRegistrationMode_UnaffectedWithoutEmailFallback(t *testing.T) {
	h := NewAuthHandler(&config.Config{
		OIDCAuth: &config.OIDCAuthConfig{Enable: true},
		Auth:     &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeSelfServe},
	}, nil, nil, nil, nil)

	if got := h.resolveRegistrationMode(context.Background()); got != config.AuthRegistrationModeSelfServe {
		t.Fatalf("registration mode = %q, want self_serve", got)
	}
}

// TestIsEmailInFallbackDomain pins the domain-matching semantics: an
// email is "in the fallback domain" iff it ends with "@domain" exactly
// (case-insensitive); subdomains are NOT included because synthesized
// addresses are always "<sub>@domain" with no subdomain.
func TestIsEmailInFallbackDomain(t *testing.T) {
	cases := []struct {
		name   string
		email  string
		domain string
		want   bool
	}{
		{"exact domain match", "abc@sjtu.edu.cn", "sjtu.edu.cn", true},
		{"case-insensitive", "ABC@SJTU.EDU.CN", "sjtu.edu.cn", true},
		{"whitespace trimmed", "  abc@sjtu.edu.cn  ", "  sjtu.edu.cn  ", true},
		{"other domain", "abc@other.com", "sjtu.edu.cn", false},
		{"subdomain not included", "abc@sub.sjtu.edu.cn", "sjtu.edu.cn", false},
		{"empty email", "", "sjtu.edu.cn", false},
		{"empty domain", "abc@sjtu.edu.cn", "", false},
		{"missing at sign", "abc", "sjtu.edu.cn", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isEmailInFallbackDomain(tc.email, tc.domain); got != tc.want {
				t.Fatalf("isEmailInFallbackDomain(%q, %q) = %v, want %v", tc.email, tc.domain, got, tc.want)
			}
		})
	}
}

// TestRegisterByInvite_RejectsFallbackDomainEmail verifies the invite
// path does not let a third party pre-register an email that an OIDC
// login would later synthesize (the account-hijack vector): with email
// fallback active, registration under the fallback domain is rejected
// before the user service is touched.
func TestRegisterByInvite_RejectsFallbackDomainEmail(t *testing.T) {
	us := &fallbackInviteUserService{}
	h := fallbackHandler(us, &fallbackInvitationService{})

	w := doRegisterByInvite(t, newRegisterByInviteRouter(h), validInviteBody("victim@sjtu.edu.cn"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("fallback-domain invite registration must return 403, got %d body=%s", w.Code, w.Body.String())
	}
	if us.registerCalled {
		t.Fatalf("UserService.Register must not be called for fallback-domain emails")
	}
}

// TestRegisterByInvite_AllowsExternalDomainEmail verifies the invite
// path still works for emails outside the fallback domain: external
// collaborators cannot use OIDC and their emails never collide with a
// synthesized address, so the gate must let them through.
func TestRegisterByInvite_AllowsExternalDomainEmail(t *testing.T) {
	us := &fallbackInviteUserService{}
	h := fallbackHandler(us, &fallbackInvitationService{})

	w := doRegisterByInvite(t, newRegisterByInviteRouter(h), validInviteBody("alice@external.com"))
	if w.Code != http.StatusCreated {
		t.Fatalf("external-domain invite registration must succeed, got %d body=%s", w.Code, w.Body.String())
	}
	if !us.registerCalled || us.registerEmail != "alice@external.com" {
		t.Fatalf("expected register with alice@external.com, called=%v email=%q", us.registerCalled, us.registerEmail)
	}
}

// TestRegisterByInvite_UnaffectedWithoutEmailFallback verifies the gate
// is scoped to the fallback mode: a plain OIDC setup keeps the historical
// invite registration behaviour for any email.
func TestRegisterByInvite_UnaffectedWithoutEmailFallback(t *testing.T) {
	us := &fallbackInviteUserService{}
	h := NewAuthHandler(&config.Config{
		OIDCAuth: &config.OIDCAuthConfig{Enable: true},
		Auth:     &config.AuthConfig{RegistrationMode: config.AuthRegistrationModeInviteOnly},
	}, us, &fallbackInviteTenantService{}, nil, &fallbackInvitationService{})

	w := doRegisterByInvite(t, newRegisterByInviteRouter(h), validInviteBody("victim@sjtu.edu.cn"))
	if w.Code != http.StatusCreated {
		t.Fatalf("invite registration must succeed when fallback is off, got %d body=%s", w.Code, w.Body.String())
	}
}
