package lti

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// NewAuditSink adapts the audit log service into the narrow AuditSink
// contract. A nil audit service degrades to a no-op sink, keeping the LTI
// package deployment-agnostic: deployments without an audit service boot and
// run, they just don't emit audit rows.
func NewAuditSink(svc interfaces.AuditLogService) AuditSink {
	if svc == nil {
		return nilSink{}
	}
	return svc
}

type nilSink struct{}

func (nilSink) Log(context.Context, *types.AuditLog) error { return nil }
