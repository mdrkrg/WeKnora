package lti

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func handoffHandler(t *testing.T, tickets TicketService, minter TokenMinter, audit AuditSink) *Handler {
	t.Helper()
	return testLTIHandler(t, &handlerDeps{
		cfg: &config.LTIConfig{
			Enable:              true,
			HandoffURL:          "https://app.example.com/api/auth/lti/handoff",
			HandoffSharedSecret: "redeem-secret",
			LaunchURL:           "https://tool.example.com/lti/launch",
			FrameAncestors:      "'self'",
			NonceMaxAge:         10 * time.Minute,
			TicketTTL:           120 * time.Second,
			SelfHandoffEnable:   true,
		},
		tickets: tickets,
		minter:  minter,
		audit:   audit,
	})
}

func TestHandoffMinterErrorRedirectsServerError(t *testing.T) {
	minter := &fakeMinter{defaultErr: errors.New("mint failed")}
	tickets := &fakeTicketService{consumeRes: &Ticket{UserID: "weknora-user-1", ContextID: "ctx-1"}}
	audit := &fakeAuditSink{}
	h := handoffHandler(t, tickets, minter, audit)

	w := getHandoff(t, h, "raw-1")
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/#lti_error=server_error")
	assertHandoffDenyAudit(t, audit, "server_error", "weknora-user-1", "ctx-1")
}

func TestHandoffNoWorkspaceRedirectsNoWorkspace(t *testing.T) {
	minter := &fakeMinter{defaultErr: ErrNoWorkspace}
	tickets := &fakeTicketService{consumeRes: &Ticket{UserID: "weknora-user-1", ContextID: "ctx-1"}}
	audit := &fakeAuditSink{}
	h := handoffHandler(t, tickets, minter, audit)

	w := getHandoff(t, h, "raw-1")
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/#lti_error=no_workspace")
	assertHandoffDenyAudit(t, audit, "no_workspace", "weknora-user-1", "ctx-1")
}

// assertHandoffDenyAudit pins the mint-failure denial audit: one row, the
// redeem-denied action, and reason/user_id/context_id in the details payload.
func assertHandoffDenyAudit(
	t *testing.T, audit *fakeAuditSink, reason, userID, contextID string,
) {
	t.Helper()
	require.Len(t, audit.entries, 1)
	entry := audit.entries[0]
	require.Equal(t, AuditActionLTITicketRedeemDenied, entry.Action)
	require.Equal(t, userID, entry.ActorUserID)
	require.Equal(t, types.AuditOutcomeDenied, entry.Outcome)
	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(entry.Details), &details))
	require.Equal(t, reason, details["reason"])
	require.Equal(t, userID, details["user_id"])
	require.Equal(t, contextID, details["context_id"])
}

func TestHandoffExpiredTicketRedirectsInvalidTicket(t *testing.T) {
	tickets := &fakeTicketService{consumeErr: ErrTicketExpired}
	h := handoffHandler(t, tickets, nil, nil)

	w := getHandoff(t, h, "raw-1")
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/#lti_error=invalid_ticket")
}

func TestHandoffUnknownTicketRedirectsInvalidTicket(t *testing.T) {
	tickets := &fakeTicketService{consumeErr: ErrTicketNotFound}
	h := handoffHandler(t, tickets, nil, nil)

	w := getHandoff(t, h, "raw-1")
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/#lti_error=invalid_ticket")
}
