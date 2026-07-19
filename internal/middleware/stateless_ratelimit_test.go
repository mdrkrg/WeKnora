// Tests for the stateless chat rate limit middleware.
// Reference: docs/stateless-chat-spec.md, section 5.1
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatelessRateLimit_AllowsUpTo60(t *testing.T) {
	ResetStatelessRateLimiter()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.POST("/test", seedTenant(1), StatelessRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	for i := 0; i < 60; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code,
			"request %d should be allowed (within 60/min limit)", i+1)
	}
}

func TestStatelessRateLimit_Rejects61stRequest(t *testing.T) {
	ResetStatelessRateLimiter()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.POST("/test", seedTenant(1), StatelessRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	for i := 0; i < 60; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code,
		"61st request must return 429 (spec section 5.1)")
}

func TestStatelessRateLimit_RespectsPerTenant(t *testing.T) {
	ResetStatelessRateLimiter()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.POST("/test/:id", seedParamTenant, StatelessRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	for i := 0; i < 60; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/test/1", nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test/2", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code,
		"tenant 2 should have independent rate limit budget (spec section 9 constraint 2)")
}

func TestStatelessRateLimit_NoTenant_SkipsLimiter(t *testing.T) {
	ResetStatelessRateLimiter()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.POST("/test", StatelessRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	for i := 0; i < 70; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/test", nil)
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}
}

func seedTenant(id uint64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), id)
		c.Next()
	}
}

func seedParamTenant(c *gin.Context) {
	id := c.Param("id")
	if id == "1" {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
	} else {
		c.Set(types.TenantIDContextKey.String(), uint64(2))
	}
	c.Next()
}
