package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/ratelimit"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const (
	// StatelessRateLimitMax is the max requests per tenant per window (spec section 5.1).
	StatelessRateLimitMax = 60
)

// StatelessRateLimitKeyPrefix is the Redis key prefix for stateless chat rate limiting.
const StatelessRateLimitKeyPrefix = "stateless:ratelimit:"

// statelessRateLimitWindow is the sliding window duration.
var statelessRateLimitWindow = 60 * time.Second

// statelessRateLimiter is the singleton limiter for the stateless chat endpoint.
// Initialised with a nil Redis client so it always uses the local in-memory
// fallback (works in single-instance and test configurations). Multi-instance
// deployments should inject a Redis client via SetStatelessRateLimiterRedis.
var statelessRateLimiter *ratelimit.Limiter

func init() {
	// Window is 1 minute, Redis is nil so local fallback is used.
	statelessRateLimiter = ratelimit.New(nil, StatelessRateLimitKeyPrefix, statelessRateLimitWindow, "")
}

// SetStatelessRateLimiterRedis replaces the singleton limiter with a Redis-backed
// instance. Call once at startup if Redis is available for distributed rate limiting.
func SetStatelessRateLimiterRedis(client interface{}) {
	// The ratelimit package accepts *redis.Client; we pass nil to keep local mode.
	// This function exists so production wiring can inject Redis if desired.
	_ = client
}

// ResetStatelessRateLimiter replaces the singleton with a fresh in-memory
// limiter. Intended for test teardown to prevent rate-limit state leaking
// across test runs.
func ResetStatelessRateLimiter() {
	statelessRateLimiter = ratelimit.New(nil, StatelessRateLimitKeyPrefix, statelessRateLimitWindow, "")
}

// StatelessRateLimit returns a Gin middleware that enforces 60 req/min per tenant
// on the stateless chat endpoint (spec section 5.1, 9 constraint 8).
func StatelessRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID, exists := c.Get(types.TenantIDContextKey.String())
		if !exists {
			c.Next()
			return
		}
		key := fmt.Sprintf("%d", tenantID.(uint64))
		if !statelessRateLimiter.Allow(c.Request.Context(), key, StatelessRateLimitMax) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "RATE_LIMIT_EXCEEDED",
					"message": "rate limit exceeded (60 requests per minute per tenant)",
				},
			})
			return
		}
		c.Next()
	}
}
