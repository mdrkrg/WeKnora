package lti

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
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
	tickets := &fakeTicketService{consumeRes: &Ticket{UserID: "weknora-user-1"}}
	h := handoffHandler(t, tickets, minter, nil)

	w := getHandoff(t, h, "raw-1")
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/#lti_error=server_error")
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
