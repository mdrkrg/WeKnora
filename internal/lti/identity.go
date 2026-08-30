package lti

import "context"

type disabledIdentityResolver struct{}

// NewDisabledIdentityResolver is the default IdentityResolver: launches are
// refused until a real resolver is wired in (the deployment wires its own
// four-step matcher).
func NewDisabledIdentityResolver() IdentityResolver {
	return disabledIdentityResolver{}
}

func (disabledIdentityResolver) Resolve(context.Context, *LaunchIdentity) (*IdentityResolution, error) {
	return nil, ErrIdentityDisabled
}
