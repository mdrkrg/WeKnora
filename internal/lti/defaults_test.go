package lti

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDisabledTokenMinterRejects(t *testing.T) {
	m := NewDisabledTokenMinter()
	_, err := m.IssueDefault(context.Background(), "u1")
	require.ErrorIs(t, err, ErrTokenMinterDisabled)
	_, err = m.IssueForTenant(context.Background(), "u1", 1)
	require.ErrorIs(t, err, ErrTokenMinterDisabled)
}

func TestDisabledIdentityResolverRejects(t *testing.T) {
	r := NewDisabledIdentityResolver()
	_, err := r.Resolve(context.Background(), &LaunchIdentity{Sub: "s1"})
	require.ErrorIs(t, err, ErrIdentityDisabled)
}

func TestNilAuditSinkIsNoop(t *testing.T) {
	sink := NewAuditSink(nil)
	require.NoError(t, sink.Log(context.Background(), &types.AuditLog{Action: AuditActionLTITicketIssued}))
}

type fakeAuditLogService struct{ entries []*types.AuditLog }

func (f *fakeAuditLogService) Log(_ context.Context, entry *types.AuditLog) error {
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakeAuditLogService) LogDenied(
	context.Context, *gin.Context, uint64, string, string, types.TenantRole,
) error {
	return nil
}

func (f *fakeAuditLogService) List(context.Context, uint64, *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
	return nil, nil
}

func (f *fakeAuditLogService) Purge(context.Context, int) (int64, error) {
	return 0, nil
}

func TestAuditSinkForwards(t *testing.T) {
	svc := &fakeAuditLogService{}
	sink := NewAuditSink(svc)
	entry := &types.AuditLog{Action: AuditActionLTITicketRedeemed}
	require.NoError(t, sink.Log(context.Background(), entry))
	require.Len(t, svc.entries, 1)
	require.Equal(t, entry, svc.entries[0])
}
