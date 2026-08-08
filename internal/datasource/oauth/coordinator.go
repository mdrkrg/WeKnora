package oauth

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// CanvasRefreshLockTTL is the expiry of the cross-replica OAuth refresh
	// lock acquired before rotating tokens.
	CanvasRefreshLockTTL = 2 * time.Minute

	canvasRefreshLockPoll      = 100 * time.Millisecond
	canvasRefreshUnlockTimeout = 3 * time.Second

	canvasDistributedRatePerSecond = 10
	canvasDistributedRateBurst     = 10
	canvasDistributedRateStateTTL  = 5 * time.Second
)

// CanvasRefreshLockKey returns the Redis key guarding OAuth token rotation
// for a data source.
func CanvasRefreshLockKey(dsID string) string { return "canvas:oauth-refresh-lock:" + dsID }

// CanvasRateLimitKey returns the Redis key of the shared token bucket
// limiting outbound Canvas API traffic for a data source.
func CanvasRateLimitKey(dsID string) string { return "canvas:rate-limit:" + dsID }

var releaseCanvasRefreshLockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
end
return 0
`)

// canvasDistributedRateLimitScript implements a token bucket shared by all
// app replicas. Redis TIME supplies a single clock so hosts with slightly
// different wall clocks cannot create extra capacity.
//
// A return value of 0 means that one request token was consumed. A positive
// value is the number of milliseconds the caller should wait before retrying.
var canvasDistributedRateLimitScript = redis.NewScript(`
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])
local redis_time = redis.call("TIME")
local now_ms = tonumber(redis_time[1]) * 1000 + math.floor(tonumber(redis_time[2]) / 1000)

local state = redis.call("HMGET", key, "tokens", "updated_at_ms")
local tokens = tonumber(state[1])
local updated_at_ms = tonumber(state[2])
if tokens == nil or updated_at_ms == nil then
  tokens = burst
  updated_at_ms = now_ms
end

local elapsed_ms = math.max(0, now_ms - updated_at_ms)
tokens = math.min(burst, tokens + elapsed_ms * rate / 1000)

if tokens >= 1 then
  tokens = tokens - 1
  redis.call("HSET", key, "tokens", tokens, "updated_at_ms", now_ms)
  redis.call("PEXPIRE", key, ttl_ms)
  return 0
end

local wait_ms = math.ceil((1 - tokens) * 1000 / rate)
redis.call("HSET", key, "tokens", tokens, "updated_at_ms", now_ms)
redis.call("PEXPIRE", key, ttl_ms)
return wait_ms
`)

// Coordinator serializes OAuth token rotation and shapes outbound connector
// traffic across app replicas. A nil Coordinator (single-process or Lite
// deployments) makes connectors fall back to their process-local singleflight
// and rate limiter.
type Coordinator interface {
	// AcquireLock acquires the key-scoped lock, blocking until it is held or
	// ctx is done. The returned release must be called by the connector.
	AcquireLock(ctx context.Context, key string, ttl time.Duration) (release func(), err error)
	// WaitRateLimit blocks until a request token is available for key.
	WaitRateLimit(ctx context.Context, key string) error
}

// RedisCoordinator implements Coordinator with a Redis-backed distributed
// lock and a shared token bucket.
type RedisCoordinator struct {
	rdb *redis.Client
}

// NewRedisCoordinator constructs a Redis-backed Coordinator.
func NewRedisCoordinator(rdb *redis.Client) *RedisCoordinator {
	return &RedisCoordinator{rdb: rdb}
}

// AcquireLock implements Coordinator.
func (c *RedisCoordinator) AcquireLock(ctx context.Context, key string, ttl time.Duration) (func(), error) {
	owner := uuid.NewString()
	ticker := time.NewTicker(canvasRefreshLockPoll)
	defer ticker.Stop()

	for {
		acquired, err := c.rdb.SetNX(ctx, key, owner, ttl).Result()
		if err != nil {
			return nil, err
		}
		if acquired {
			logger.Infof(ctx, "acquired distributed Canvas refresh lock for key=%s", key)
			return func() {
				unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), canvasRefreshUnlockTimeout)
				defer cancel()
				if _, err := releaseCanvasRefreshLockScript.Run(unlockCtx, c.rdb, []string{key}, owner).Result(); err != nil {
					logger.Warnf(ctx, "failed to release Canvas refresh lock for key=%s: %v", key, err)
				}
			}, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// WaitRateLimit implements Coordinator.
func (c *RedisCoordinator) WaitRateLimit(ctx context.Context, key string) error {
	for {
		waitMillis, err := canvasDistributedRateLimitScript.Run(
			ctx,
			c.rdb,
			[]string{key},
			canvasDistributedRatePerSecond,
			canvasDistributedRateBurst,
			canvasDistributedRateStateTTL.Milliseconds(),
		).Int64()
		if err != nil {
			return fmt.Errorf("acquire distributed Canvas request token: %w", err)
		}
		if waitMillis <= 0 {
			return nil
		}

		timer := time.NewTimer(time.Duration(waitMillis) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
