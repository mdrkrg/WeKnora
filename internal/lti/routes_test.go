package lti

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterPublicRoutesMountsLTIEndpoints(t *testing.T) {
	h := testLTIHandler(t, nil)
	r := gin.New()
	RegisterPublicRoutes(r, h)

	want := map[string]bool{
		"/lti/login_initiations": true,
		"/lti/launch":            true,
		"/.well-known/jwks.json": true,
		"/lti/tickets/redeem":    true,
	}
	for _, rt := range r.Routes() {
		if want[rt.Path] {
			delete(want, rt.Path)
		}
	}
	require.Empty(t, want, "unmounted public routes")
}
