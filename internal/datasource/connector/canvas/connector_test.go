package canvas

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

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

// fakeCanvas serves folder listing + file metadata/download endpoints and
// counts GetFile / download hits so tests can assert API thrift.
type fakeCanvas struct {
	server         *httptest.Server
	files          map[int64]canvasFile
	getFileHits    atomic.Int64
	downloadHits   atomic.Int64
	downloadByFile map[int64]*atomic.Int64
}

func newFakeCanvas(t *testing.T, folderID int64, files []canvasFile) *fakeCanvas {
	t.Helper()
	f := &fakeCanvas{
		files:          map[int64]canvasFile{},
		downloadByFile: map[int64]*atomic.Int64{},
	}
	for _, file := range files {
		cp := file
		f.files[cp.ID] = cp
		f.downloadByFile[cp.ID] = &atomic.Int64{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v1/folders/%d/files", folderID), func(w http.ResponseWriter, r *http.Request) {
		listed := make([]canvasFile, 0, len(files))
		for _, file := range files {
			cp := file
			if cp.URL == "" {
				cp.URL = f.server.URL + "/download/" + strconv.FormatInt(cp.ID, 10)
			}
			listed = append(listed, cp)
		}
		_ = json.NewEncoder(w).Encode(listed)
	})
	mux.HandleFunc(fmt.Sprintf("/api/v1/folders/%d/folders", folderID), func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]canvasFolder{})
	})
	mux.HandleFunc("/api/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/files/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		meta, ok := f.files[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.getFileHits.Add(1)
		if meta.URL == "" {
			meta.URL = f.server.URL + "/download/" + idStr
		}
		_ = json.NewEncoder(w).Encode(meta)
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/download/")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f.downloadHits.Add(1)
		if c, ok := f.downloadByFile[id]; ok {
			c.Add(1)
		}
		_, _ = w.Write([]byte("content-" + idStr))
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	// Rewrite download URLs now that the server URL is known.
	for id, meta := range f.files {
		meta.URL = f.server.URL + "/download/" + strconv.FormatInt(id, 10)
		f.files[id] = meta
	}
	return f
}

func (f *fakeCanvas) config(resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		ResourceIDs: resourceIDs,
		Credentials: map[string]interface{}{
			"base_url":      f.server.URL,
			"client_id":     "cid",
			"client_secret": "csecret",
			"access_token":  "tok",
		},
	}
}

func TestFetchAll_UsesListMetaWithoutExtraGetFile(t *testing.T) {
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	f := newFakeCanvas(t, 10, []canvasFile{
		{ID: 1, DisplayName: "a.pdf", ContentType: "application/pdf", UpdatedAt: old, FolderID: 10},
		{ID: 2, DisplayName: "b.pdf", ContentType: "application/pdf", UpdatedAt: old, FolderID: 10},
	})

	items, err := NewConnector().FetchAll(context.Background(), f.config(nil), []string{"folder:10"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d want 2", len(items))
	}
	if got := f.getFileHits.Load(); got != 0 {
		t.Fatalf("FetchAll must reuse ListFiles meta; GetFile hits=%d want 0", got)
	}
	if got := f.downloadHits.Load(); got != 2 {
		t.Fatalf("download hits=%d want 2", got)
	}
}

func TestFetchIncremental_SkipsUnchangedDownloads(t *testing.T) {
	unchangedAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	changedAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeCanvas(t, 10, []canvasFile{
		{ID: 1, DisplayName: "old.pdf", ContentType: "application/pdf", UpdatedAt: unchangedAt.Format(time.RFC3339), FolderID: 10},
		{ID: 2, DisplayName: "new.pdf", ContentType: "application/pdf", UpdatedAt: changedAt.Format(time.RFC3339), FolderID: 10},
	})
	cfg := f.config([]string{"folder:10"})
	cursor := &types.SyncCursor{LastSyncTime: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)}

	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if next == nil || !next.LastSyncTime.After(cursor.LastSyncTime) {
		t.Fatalf("expected advanced cursor, got %#v", next)
	}
	if len(items) != 1 || items[0].ExternalID != "file:2" {
		t.Fatalf("items=%v want only file:2", items)
	}
	if got := f.downloadByFile[1].Load(); got != 0 {
		t.Fatalf("unchanged file downloaded %d times", got)
	}
	if got := f.downloadByFile[2].Load(); got != 1 {
		t.Fatalf("changed file download hits=%d want 1", got)
	}
	if got := f.getFileHits.Load(); got != 0 {
		t.Fatalf("incremental folder sync must not GetFile when list meta has url; hits=%d", got)
	}
}

func TestFetchIncremental_DirectFileUsesSingleGetFile(t *testing.T) {
	updated := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeCanvas(t, 10, []canvasFile{
		{ID: 99, DisplayName: "solo.pdf", ContentType: "application/pdf", UpdatedAt: updated.Format(time.RFC3339), FolderID: 10},
	})
	cfg := f.config([]string{"file:99"})
	cursor := &types.SyncCursor{LastSyncTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}

	items, _, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d want 1", len(items))
	}
	if got := f.getFileHits.Load(); got != 1 {
		t.Fatalf("direct file resource should GetFile once, got %d", got)
	}
	if got := f.downloadHits.Load(); got != 1 {
		t.Fatalf("download hits=%d want 1", got)
	}

	f.getFileHits.Store(0)
	f.downloadHits.Store(0)
	f.downloadByFile[99].Store(0)
	cursor2 := &types.SyncCursor{LastSyncTime: updated.Add(time.Hour)}
	items2, _, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor2)
	if err != nil {
		t.Fatalf("second FetchIncremental: %v", err)
	}
	if len(items2) != 0 {
		t.Fatalf("expected no items after cursor past updated_at, got %d", len(items2))
	}
	if got := f.getFileHits.Load(); got != 1 {
		t.Fatalf("unchanged direct file still needs one GetFile for updated_at, got %d", got)
	}
	if got := f.downloadHits.Load(); got != 0 {
		t.Fatalf("unchanged direct file must not download, got %d", got)
	}
}

func TestDownloadFromMeta_401RefreshesAndRetries(t *testing.T) {
	var downloadHits atomic.Int64
	var refreshHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Fatalf("unexpected grant_type %q", r.Form.Get("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	mux.HandleFunc("/files/1/download", func(w http.ResponseWriter, r *http.Request) {
		downloadHits.Add(1)
		auth := r.Header.Get("Authorization")
		if auth != "Bearer new-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "file-bytes")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli, err := NewClient(&Config{
		BaseURL:      srv.URL,
		ClientID:     "cid",
		ClientSecret: "sec",
		AccessToken:  "old-tok",
		RefreshToken: "ref-1",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	data, name, ct, err := cli.DownloadFromMeta(context.Background(), &canvasFile{
		ID:          1,
		DisplayName: "a.txt",
		ContentType: "text/plain",
		URL:         srv.URL + "/files/1/download",
	})
	if err != nil {
		t.Fatalf("DownloadFromMeta: %v", err)
	}
	if string(data) != "file-bytes" || name != "a.txt" || ct != "text/plain" {
		t.Fatalf("got data=%q name=%q ct=%q", data, name, ct)
	}
	if downloadHits.Load() != 2 {
		t.Fatalf("download hits=%d want 2 (401 then retry)", downloadHits.Load())
	}
	if refreshHits.Load() != 1 {
		t.Fatalf("refresh hits=%d want 1", refreshHits.Load())
	}
	if cli.cfg.AccessToken != "new-tok" {
		t.Fatalf("token not rotated, got %q", cli.cfg.AccessToken)
	}
}
