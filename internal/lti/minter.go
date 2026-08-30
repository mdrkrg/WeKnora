package lti

import "context"

type disabledTokenMinter struct{}

// NewDisabledTokenMinter is the default TokenMinter: redemption is refused
// until a real minter is wired in (the deployment wires its own user-service
// token issuance).
func NewDisabledTokenMinter() TokenMinter {
	return disabledTokenMinter{}
}

func (disabledTokenMinter) IssueDefault(context.Context, string) (*TokenResult, error) {
	return nil, ErrTokenMinterDisabled
}
