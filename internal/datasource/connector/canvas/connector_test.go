package canvas

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseResourceID(t *testing.T) {
	kind, id, err := parseResourceID("course:42")
	if err != nil || kind != resourceTypeCourse || id != 42 {
		t.Fatalf("course parse: kind=%s id=%d err=%v", kind, id, err)
	}
	kind, id, err = parseResourceID("folder:7")
	if err != nil || kind != resourceTypeFolder || id != 7 {
		t.Fatalf("folder parse: kind=%s id=%d err=%v", kind, id, err)
	}
	kind, id, err = parseResourceID("file:99")
	if err != nil || kind != resourceTypeFile || id != 99 {
		t.Fatalf("file parse: kind=%s id=%d err=%v", kind, id, err)
	}
}

func TestParseCanvasConfigRequiresAppWhenEnforced(t *testing.T) {
	_, err := parseCanvasConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"base_url":  "https://canvas.example.com",
			"client_id": "1",
		},
	}, true)
	if err == nil {
		t.Fatal("expected error for missing client_secret when requireApp=true")
	}

	cfg, err := parseCanvasConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"access_token": "tok",
		},
	}, false)
	if err != nil {
		t.Fatalf("OAuth parse should allow empty app fields: %v", err)
	}
	if cfg.AccessToken != "tok" {
		t.Fatalf("expected access_token, got %#v", cfg)
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	u := BuildAuthorizeURL("http://localhost:3080/", "10000000000005", "http://localhost:5173/api/v1/datasource/oauth/callback", "abc")
	if u == "" || u[:4] != "http" {
		t.Fatalf("unexpected url %q", u)
	}
	if want := "client_id=10000000000005"; !contains(u, want) {
		t.Fatalf("missing %s in %s", want, u)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestConnectorType(t *testing.T) {
	c := NewConnector()
	if c.Type() != types.ConnectorTypeCanvas {
		t.Fatalf("type=%s", c.Type())
	}
}

func TestReadWithLimitRejectsOversizedContent(t *testing.T) {
	if _, err := readWithLimit(bytes.NewBufferString("12345"), 4); err == nil {
		t.Fatal("expected oversized content to be rejected")
	}

	data, err := readWithLimit(bytes.NewBufferString("1234"), 4)
	if err != nil {
		t.Fatalf("exact-limit content failed: %v", err)
	}
	if string(data) != "1234" {
		t.Fatalf("unexpected data %q", data)
	}
}

func TestNextIncrementalCursorKeepsHighWaterMarkOnPartialFailure(t *testing.T) {
	previous := &types.SyncCursor{LastSyncTime: time.Unix(100, 0).UTC()}
	now := time.Unix(200, 0).UTC()
	partial := &datasource.PartialFetchError{Details: []string{"file 2 failed"}}

	if got := nextIncrementalCursor(previous, partial, now); got != previous {
		t.Fatalf("partial failure advanced cursor: got %#v want %#v", got, previous)
	}
	if got := nextIncrementalCursor(nil, partial, now); got != nil {
		t.Fatalf("first partial sync should keep a nil cursor, got %#v", got)
	}
	if got := nextIncrementalCursor(previous, nil, now); got == nil || !got.LastSyncTime.Equal(now) {
		t.Fatalf("successful sync did not advance cursor: %#v", got)
	}
}

func TestTokenCredentialsMapExcludesClientSecret(t *testing.T) {
	cfg := &Config{
		BaseURL:      "https://canvas.example.com",
		ClientID:     "client-id",
		ClientSecret: "super-secret",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    "2026-01-01T00:00:00Z",
	}

	m := cfg.TokenCredentialsMap()
	if _, ok := m["client_secret"]; ok {
		t.Fatalf("TokenCredentialsMap must not include client_secret, got %#v", m)
	}
	if m["access_token"] != "access-token" || m["refresh_token"] != "refresh-token" {
		t.Fatalf("TokenCredentialsMap returned unexpected tokens: %#v", m)
	}
}

func TestWalkFolderAncestorsSkipsInvisibleRootAndAddsCourse(t *testing.T) {
	rootID := int64(10)
	parentID := int64(20)
	folders := map[int64]canvasFolder{
		10: {ID: 10, Name: "course files", ContextType: "Course", ContextID: 7},
		20: {ID: 20, Name: "parent", ParentID: &rootID},
		30: {ID: 30, Name: "selected", ParentID: &parentID},
	}
	client := &Client{
		cfg: &Config{BaseURL: "https://canvas.example.com", AccessToken: "token"},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			const prefix = "/api/v1/folders/"
			id, err := strconv.ParseInt(req.URL.Path[len(prefix):], 10, 64)
			if err != nil {
				return nil, err
			}
			body, err := json.Marshal(folders[id])
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
				Request:    req,
			}, nil
		})},
	}

	var got []string
	NewConnector().walkFolderAncestors(context.Background(), client, 30, func(id string) {
		got = append(got, id)
	})
	want := []string{"folder:20", "course:7"}
	if len(got) != len(want) {
		t.Fatalf("ancestors=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ancestors=%v want=%v", got, want)
		}
	}
}
