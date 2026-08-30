package lti

import (
	"context"
	"errors"
	"time"

	"github.com/MicahParks/keyfunc/v3"
)

type fakeKeysets struct {
	kf           keyfunc.Keyfunc
	refreshCalls int
	refreshed    bool
}

func (f *fakeKeysets) Resolve(_ context.Context, _ *Registration) (keyfunc.Keyfunc, error) {
	if f.kf != nil {
		return f.kf, nil
	}
	return nil, errors.New("no keyset")
}

func (f *fakeKeysets) Refresh(_ context.Context, _ *Registration) (keyfunc.Keyfunc, error) {
	f.refreshCalls++
	if f.refreshed {
		return f.kf, nil
	}
	return nil, errors.New("no keyset")
}

type fakeRegistrationStore struct {
	regs        []*Registration
	savedKeyset []string
}

func (f *fakeRegistrationStore) GetByIssuerAndClientID(
	_ context.Context, issuer, clientID string,
) (*Registration, error) {
	for _, r := range f.regs {
		if r.Issuer == issuer && r.ClientID == clientID {
			return r, nil
		}
	}
	return nil, nil
}

func (f *fakeRegistrationStore) GetByID(_ context.Context, id uint64) (*Registration, error) {
	for _, r := range f.regs {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}

func (f *fakeRegistrationStore) SaveKeyset(_ context.Context, _ uint64, raw string, _ time.Time) error {
	f.savedKeyset = append(f.savedKeyset, raw)
	return nil
}

type fakeToolKeyStore struct {
	key *ToolKey
	err error
}

func (f *fakeToolKeyStore) Ensure(_ context.Context) (*ToolKey, error) {
	return f.key, f.err
}

type fakeTicketService struct {
	raw        string
	issueErr   error
	issueArgs  []string
	issueRoles []string
	consumeRes *Ticket
	consumeErr error
	deleteRes  int64
	deleteErr  error
}

func (f *fakeTicketService) Issue(_ context.Context, userID, contextID string, roles []string) (string, error) {
	f.issueArgs = []string{userID, contextID}
	f.issueRoles = roles
	return f.raw, f.issueErr
}

func (f *fakeTicketService) Consume(_ context.Context, _ string) (*Ticket, error) {
	return f.consumeRes, f.consumeErr
}

func (f *fakeTicketService) DeleteExpired(_ context.Context, _ time.Time) (int64, error) {
	return f.deleteRes, f.deleteErr
}

type fakeResolver struct {
	res *IdentityResolution
	err error
}

func (f *fakeResolver) Resolve(_ context.Context, _ *LaunchIdentity) (*IdentityResolution, error) {
	return f.res, f.err
}

type fakeMinter struct {
	defaultResult  *TokenResult
	defaultErr     error
	forTenantRes   *TokenResult
	forTenantErr   error
	lastTenantID   uint64
	lastDefaultUID string
}

func (f *fakeMinter) IssueDefault(_ context.Context, userID string) (*TokenResult, error) {
	f.lastDefaultUID = userID
	return f.defaultResult, f.defaultErr
}

func (f *fakeMinter) IssueForTenant(_ context.Context, _ string, tenantID uint64) (*TokenResult, error) {
	f.lastTenantID = tenantID
	return f.forTenantRes, f.forTenantErr
}

type fakeTicketStore struct {
	tickets []*Ticket
	now     func() time.Time
}

func (f *fakeTicketStore) Create(_ context.Context, t *Ticket) error {
	f.tickets = append(f.tickets, t)
	return nil
}

func (f *fakeTicketStore) Consume(_ context.Context, tokenHash string) (*Ticket, error) {
	for _, t := range f.tickets {
		if t.TokenHash != tokenHash {
			continue
		}
		now := time.Now()
		if f.now != nil {
			now = f.now()
		}
		if now.After(t.ExpiresAt) {
			return nil, ErrTicketExpired
		}
		if t.ConsumedAt != nil {
			return nil, ErrTicketConsumed
		}
		t.ConsumedAt = &now
		return t, nil
	}
	return nil, ErrTicketNotFound
}

func (f *fakeTicketStore) DeleteExpired(_ context.Context, cutoff time.Time) (int64, error) {
	kept := f.tickets[:0]
	var deleted int64
	for _, t := range f.tickets {
		if t.ExpiresAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, t)
	}
	f.tickets = kept
	return deleted, nil
}
