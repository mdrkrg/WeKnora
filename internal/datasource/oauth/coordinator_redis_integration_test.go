package oauth

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	canvasconnector "github.com/Tencent/WeKnora/internal/datasource/connector/canvas"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func canvasRedisTestClient() *redis.Client {
	db, _ := strconv.Atoi(os.Getenv("REDIS_DB"))
	return redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_ADDR"),
		Username: os.Getenv("REDIS_USERNAME"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       db,
	})
}

func TestCanvasRedisRefreshHelperProcess(t *testing.T) {
	if os.Getenv("WEKNORA_CANVAS_REDIS_HELPER") != "1" {
		return
	}
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rdb := canvasRedisTestClient()
	defer rdb.Close()

	dsID := os.Getenv("WEKNORA_CANVAS_REDIS_DS_ID")
	credentialsKey := os.Getenv("WEKNORA_CANVAS_REDIS_CREDENTIALS_KEY")
	coord := NewRedisCoordinator(rdb)
	config := &types.DataSourceConfig{
		Type: types.ConnectorTypeCanvas,
		Credentials: map[string]interface{}{
			"base_url":      os.Getenv("WEKNORA_CANVAS_REDIS_BASE_URL"),
			"client_id":     "cid",
			"client_secret": "sec",
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
			"expires_at":    time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		},
		RuntimeDataSourceID: dsID,
	}
	config.AcquireCredentialRefreshLock = func(ctx context.Context) (func(), error) {
		return coord.AcquireLock(ctx, CanvasRefreshLockKey(dsID), CanvasRefreshLockTTL)
	}
	config.OnCredentialsReload = func(ctx context.Context) (map[string]interface{}, error) {
		stored, err := rdb.HGetAll(ctx, credentialsKey).Result()
		if err != nil {
			return nil, err
		}
		credentials := make(map[string]interface{}, len(stored))
		for key, value := range stored {
			credentials[key] = value
		}
		return credentials, nil
	}
	config.OnCredentialsUpdated = func(ctx context.Context, credentials map[string]interface{}) error {
		return rdb.HSet(ctx, credentialsKey, credentials).Err()
	}

	resources, err := canvasconnector.NewConnector().ListResources(ctx, config, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(resources) != 1 || resources[0].ExternalID != "course:101" {
		fmt.Fprintf(os.Stderr, "unexpected resources: %#v\n", resources)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestCanvasRedisRateLimitHelperProcess(t *testing.T) {
	if os.Getenv("WEKNORA_CANVAS_REDIS_RATE_HELPER") != "1" {
		return
	}
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rdb := canvasRedisTestClient()
	defer rdb.Close()

	dsID := os.Getenv("WEKNORA_CANVAS_REDIS_DS_ID")
	coord := NewRedisCoordinator(rdb)
	config := &types.DataSourceConfig{
		Type: types.ConnectorTypeCanvas,
		Credentials: map[string]interface{}{
			"base_url":      os.Getenv("WEKNORA_CANVAS_REDIS_BASE_URL"),
			"client_id":     "cid",
			"client_secret": "sec",
			"access_token":  "access",
			"refresh_token": "refresh",
			"expires_at":    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		RuntimeDataSourceID: dsID,
		WaitForRateLimit: func(ctx context.Context) error {
			return coord.WaitRateLimit(ctx, CanvasRateLimitKey(dsID))
		},
	}

	connector := canvasconnector.NewConnector()
	for i := 0; i < 10; i++ {
		resources, err := connector.ListResources(ctx, config, "")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if len(resources) != 1 || resources[0].ExternalID != "course:101" {
			fmt.Fprintf(os.Stderr, "unexpected resources: %#v\n", resources)
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func TestCanvasRefresh_RedisCoordinatesDifferentProcesses(t *testing.T) {
	if os.Getenv("WEKNORA_CANVAS_REDIS_INTEGRATION") != "1" {
		t.Skip("set WEKNORA_CANVAS_REDIS_INTEGRATION=1 with a reachable Redis to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rdb := canvasRedisTestClient()
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	dsID := "redis-integration-" + uuid.NewString()
	credentialsKey := "canvas:test:credentials:" + dsID
	lockKey := CanvasRefreshLockKey(dsID)
	t.Cleanup(func() { _ = rdb.Del(context.Background(), credentialsKey, lockKey).Err() })
	requireFields := map[string]interface{}{
		"access_token":  "old-access",
		"refresh_token": "old-refresh",
		"expires_at":    time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
	}
	if err := rdb.HSet(ctx, credentialsKey, requireFields).Err(); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}

	var tokenHits atomic.Int64
	var invalidRefreshHits atomic.Int64
	var tokenMu sync.Mutex
	currentAccess := "old-access"
	currentRefresh := "old-refresh"
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(500 * time.Millisecond)
		tokenMu.Lock()
		defer tokenMu.Unlock()
		if r.Form.Get("refresh_token") != currentRefresh {
			invalidRefreshHits.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"refresh token already rotated"}`)
			return
		}
		currentAccess = "new-access"
		currentRefresh = "new-refresh"
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	})
	mux.HandleFunc("/api/v1/courses", func(w http.ResponseWriter, r *http.Request) {
		tokenMu.Lock()
		expected := "Bearer " + currentAccess
		tokenMu.Unlock()
		if r.Header.Get("Authorization") != expected {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid access token"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":101,"name":"SE2303 Mock Course","course_code":"SE2303","workflow_state":"available"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	for i := range commands {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCanvasRedisRefreshHelperProcess$")
		cmd.Env = append(os.Environ(),
			"WEKNORA_CANVAS_REDIS_HELPER=1",
			"WEKNORA_CANVAS_REDIS_BASE_URL="+srv.URL,
			"WEKNORA_CANVAS_REDIS_DS_ID="+dsID,
			"WEKNORA_CANVAS_REDIS_CREDENTIALS_KEY="+credentialsKey,
			"SSRF_WHITELIST=127.0.0.1,localhost",
		)
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		commands[i] = cmd
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d failed: %v output=%s", i, err, outputs[i].String())
		}
	}
	if tokenHits.Load() != 1 || invalidRefreshHits.Load() != 0 {
		t.Fatalf("strict rotation token hits=%d invalid=%d, want 1/0", tokenHits.Load(), invalidRefreshHits.Load())
	}
	stored, err := rdb.HGetAll(ctx, credentialsKey).Result()
	if err != nil {
		t.Fatalf("read persisted credentials: %v", err)
	}
	if stored["access_token"] != "new-access" || stored["refresh_token"] != "new-refresh" {
		t.Fatalf("rotated credentials were not persisted")
	}
}

func TestCanvasRateLimit_RedisCoordinatesDifferentProcesses(t *testing.T) {
	if os.Getenv("WEKNORA_CANVAS_REDIS_INTEGRATION") != "1" {
		t.Skip("set WEKNORA_CANVAS_REDIS_INTEGRATION=1 with a reachable Redis to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rdb := canvasRedisTestClient()
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping Redis: %v", err)
	}

	dsID := "redis-rate-integration-" + uuid.NewString()
	rateKey := CanvasRateLimitKey(dsID)
	t.Cleanup(func() { _ = rdb.Del(context.Background(), rateKey).Err() })

	var requestHits atomic.Int64
	var requestTimesMu sync.Mutex
	var firstRequestAt time.Time
	var lastRequestAt time.Time
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/courses", func(w http.ResponseWriter, r *http.Request) {
		requestHits.Add(1)
		now := time.Now()
		requestTimesMu.Lock()
		if firstRequestAt.IsZero() {
			firstRequestAt = now
		}
		lastRequestAt = now
		requestTimesMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":101,"name":"SE2303 Mock Course","course_code":"SE2303","workflow_state":"available"}]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	startedAt := time.Now()
	for i := range commands {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCanvasRedisRateLimitHelperProcess$")
		cmd.Env = append(os.Environ(),
			"WEKNORA_CANVAS_REDIS_RATE_HELPER=1",
			"WEKNORA_CANVAS_REDIS_BASE_URL="+srv.URL,
			"WEKNORA_CANVAS_REDIS_DS_ID="+dsID,
			"SSRF_WHITELIST=127.0.0.1,localhost",
		)
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		commands[i] = cmd
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d failed: %v output=%s", i, err, outputs[i].String())
		}
	}
	elapsed := time.Since(startedAt)
	if requestHits.Load() != 20 {
		t.Fatalf("request hits=%d, want 20", requestHits.Load())
	}
	requestTimesMu.Lock()
	requestSpan := lastRequestAt.Sub(firstRequestAt)
	requestTimesMu.Unlock()
	if requestSpan < 700*time.Millisecond {
		t.Fatalf("20 upstream requests spanned %v; shared 10 req/s bucket was not enforced", requestSpan)
	}
	t.Logf("20 requests across two processes completed in %v; upstream request span %v", elapsed, requestSpan)
}
