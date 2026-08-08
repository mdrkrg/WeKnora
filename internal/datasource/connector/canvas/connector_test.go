package canvas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
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

func TestCanvasRefreshHelperProcess(t *testing.T) {
	if os.Getenv("WEKNORA_CANVAS_REFRESH_HELPER") != "1" {
		return
	}
	cli, err := NewClient(&Config{
		BaseURL:      os.Getenv("WEKNORA_CANVAS_HELPER_BASE_URL"),
		ClientID:     "cid",
		ClientSecret: "sec",
		DataSourceID: "ds-cross-process-refresh",
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
	}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := cli.RefreshAccessToken(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
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
	current := map[string]map[string]string{
		"folder:10": {"file:1": "2024-01-01T00:00:00Z"},
	}

	got := nextIncrementalCursor(previous, nil, current, nil, partial, now)
	if got == nil {
		t.Fatal("expected cursor on partial failure")
	}
	if !got.LastSyncTime.Equal(previous.LastSyncTime) {
		t.Fatalf("partial failure advanced LastSyncTime: got %v want %v", got.LastSyncTime, previous.LastSyncTime)
	}
	prevCursor := parseCanvasCursor(got)
	if prevCursor == nil || prevCursor.ResourceFiles["folder:10"]["file:1"] == "" {
		t.Fatalf("partial failure should still refresh file inventory: %#v", got.ConnectorCursor)
	}

	if got := nextIncrementalCursor(nil, nil, nil, nil, partial, now); got == nil || !got.LastSyncTime.IsZero() {
		t.Fatalf("first partial sync should keep zero LastSyncTime, got %#v", got)
	}

	got = nextIncrementalCursor(previous, nil, current, nil, nil, now)
	if got == nil || !got.LastSyncTime.Equal(now) {
		t.Fatalf("successful sync did not advance cursor: %#v", got)
	}

	hard := fmt.Errorf("%w: boom", datasource.ErrFetchFailed)
	if got := nextIncrementalCursor(previous, nil, current, nil, hard, now); got != previous {
		t.Fatalf("hard failure should keep previous cursor pointer, got %#v", got)
	}
}

func TestDetectCanvasDeletions(t *testing.T) {
	prev := &canvasCursor{
		ResourceFiles: map[string]map[string]string{
			"folder:10": {"file:1": "t1", "file:2": "t2"},
			"folder:20": {"file:3": "t3"},
		},
	}
	current := map[string]map[string]string{
		"folder:10": {"file:1": "t1"},
	}
	items := detectCanvasDeletions(prev, current, nil)
	if len(items) != 1 || !items[0].IsDeleted || items[0].ExternalID != "file:2" {
		t.Fatalf("items=%v want only file:2 deleted", items)
	}

	// Deselected folder:20 must not emit deletions.
	for _, it := range items {
		if it.ExternalID == "file:3" {
			t.Fatal("deselected resource must not emit IsDeleted")
		}
	}

	incomplete := map[string]struct{}{"folder:10": {}}
	if got := detectCanvasDeletions(prev, current, incomplete); len(got) != 0 {
		t.Fatalf("incomplete listing must not emit deletions, got %v", got)
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
	folderID       int64
	listFilesHits  atomic.Int64
	listFolderHits atomic.Int64
	getFileHits    atomic.Int64
	downloadHits   atomic.Int64
	downloadByFile map[int64]*atomic.Int64
}

func newFakeCanvas(t *testing.T, folderID int64, files []canvasFile) *fakeCanvas {
	t.Helper()
	f := &fakeCanvas{
		files:          map[int64]canvasFile{},
		folderID:       folderID,
		downloadByFile: map[int64]*atomic.Int64{},
	}
	for _, file := range files {
		cp := file
		f.files[cp.ID] = cp
		f.downloadByFile[cp.ID] = &atomic.Int64{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/api/v1/folders/%d/files", folderID), func(w http.ResponseWriter, r *http.Request) {
		f.listFilesHits.Add(1)
		listed := make([]canvasFile, 0, len(f.files))
		for _, file := range f.files {
			cp := file
			if cp.FolderID != 0 && cp.FolderID != folderID {
				continue
			}
			if cp.URL == "" {
				cp.URL = f.server.URL + "/download/" + strconv.FormatInt(cp.ID, 10)
			}
			listed = append(listed, cp)
		}
		_ = json.NewEncoder(w).Encode(listed)
	})
	mux.HandleFunc(fmt.Sprintf("/api/v1/folders/%d/folders", folderID), func(w http.ResponseWriter, r *http.Request) {
		f.listFolderHits.Add(1)
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

func (f *fakeCanvas) removeFile(id int64) {
	delete(f.files, id)
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
	if len(items) != 2 {
		t.Fatalf("items=%v want changed and skipped files", items)
	}
	byID := make(map[string]types.FetchedItem, len(items))
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	if !byID["file:1"].IsSkipped {
		t.Fatalf("unchanged file was not reported as skipped: %#v", byID["file:1"])
	}
	if byID["file:2"].IsSkipped || len(byID["file:2"].Content) == 0 {
		t.Fatalf("changed file was not downloaded: %#v", byID["file:2"])
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

// Characterization test for the current MR behavior: incremental sync avoids
// unchanged file downloads, but it still enumerates the selected folder tree
// to obtain updated_at metadata. When a folder-scoped Canvas "since" API or a
// different inventory strategy is implemented, update this test to assert the
// new lower API-call contract.
func TestFetchIncremental_StillEnumeratesSelectedTree(t *testing.T) {
	unchangedAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeCanvas(t, 10, []canvasFile{
		{ID: 1, DisplayName: "old.pdf", ContentType: "application/pdf", UpdatedAt: unchangedAt.Format(time.RFC3339), FolderID: 10},
	})
	cfg := f.config([]string{"folder:10"})
	cursor := &types.SyncCursor{LastSyncTime: unchangedAt.Add(time.Hour)}

	items, _, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 1 || !items[0].IsSkipped || items[0].ExternalID != "file:1" {
		t.Fatalf("unchanged file should be reported as skipped, got %v", items)
	}
	if got := f.downloadHits.Load(); got != 0 {
		t.Fatalf("unchanged file should not be downloaded, got %d downloads", got)
	}
	if got := f.listFilesHits.Load(); got == 0 {
		t.Fatal("incremental sync did not enumerate the selected folder")
	}
	if got := f.listFolderHits.Load(); got == 0 {
		t.Fatal("incremental sync did not enumerate child folders")
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
	if len(items2) != 1 || !items2[0].IsSkipped || items2[0].ExternalID != "file:99" {
		t.Fatalf("expected skipped file after cursor past updated_at, got %#v", items2)
	}
	if got := f.getFileHits.Load(); got != 1 {
		t.Fatalf("unchanged direct file still needs one GetFile for updated_at, got %d", got)
	}
	if got := f.downloadHits.Load(); got != 0 {
		t.Fatalf("unchanged direct file must not download, got %d", got)
	}
}

func TestFetchIncremental_DetectsDeletion(t *testing.T) {
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeCanvas(t, 10, []canvasFile{
		{ID: 1, DisplayName: "keep.pdf", ContentType: "application/pdf", UpdatedAt: old.Format(time.RFC3339), FolderID: 10},
		{ID: 2, DisplayName: "gone.pdf", ContentType: "application/pdf", UpdatedAt: old.Format(time.RFC3339), FolderID: 10},
	})
	cfg := f.config([]string{"folder:10"})

	_, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	parsed := parseCanvasCursor(cursor)
	if parsed == nil || len(parsed.ResourceFiles["folder:10"]) != 2 {
		t.Fatalf("cursor inventory=%#v want 2 files", cursor.ConnectorCursor)
	}

	f.removeFile(2)
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	var deleted []types.FetchedItem
	for _, it := range items {
		if it.IsDeleted {
			deleted = append(deleted, it)
		}
	}
	if len(deleted) != 1 || deleted[0].ExternalID != "file:2" {
		t.Fatalf("deleted=%v want file:2", deleted)
	}
	if deleted[0].SourceResourceID != "folder:10" {
		t.Fatalf("SourceResourceID=%q", deleted[0].SourceResourceID)
	}

	nextParsed := parseCanvasCursor(next)
	if nextParsed == nil || len(nextParsed.ResourceFiles["folder:10"]) != 1 {
		t.Fatalf("next inventory=%#v want only file:1", next.ConnectorCursor)
	}
	if _, ok := nextParsed.ResourceFiles["folder:10"]["file:2"]; ok {
		t.Fatal("deleted file still in cursor inventory")
	}
}

func TestFetchIncremental_DeselectedResourceNotDeleted(t *testing.T) {
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeCanvas(t, 10, []canvasFile{
		{ID: 1, DisplayName: "a.pdf", ContentType: "application/pdf", UpdatedAt: old.Format(time.RFC3339), FolderID: 10},
	})
	cfg := f.config([]string{"folder:10"})
	_, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// User deselected folder:10; sync with empty selection should not emit IsDeleted.
	cfg.ResourceIDs = nil
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("deselected sync: %v", err)
	}
	for _, it := range items {
		if it.IsDeleted {
			t.Fatalf("deselected resource must not emit IsDeleted, got %#v", it)
		}
	}
	if parsed := parseCanvasCursor(next); parsed != nil && len(parsed.ResourceFiles) != 0 {
		t.Fatalf("deselected resource should drop from cursor, got %#v", next.ConnectorCursor)
	}
}

func TestFetchIncremental_DirectFileDeletion(t *testing.T) {
	updated := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	f := newFakeCanvas(t, 10, []canvasFile{
		{ID: 99, DisplayName: "solo.pdf", ContentType: "application/pdf", UpdatedAt: updated.Format(time.RFC3339), FolderID: 10},
	})
	cfg := f.config([]string{"file:99"})
	_, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	f.removeFile(99)
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	var deleted int
	for _, it := range items {
		if it.IsDeleted && it.ExternalID == "file:99" {
			deleted++
		}
	}
	if deleted != 1 {
		t.Fatalf("deleted count=%d want 1; items=%v", deleted, items)
	}
	if parsed := parseCanvasCursor(next); parsed == nil || len(parsed.ResourceFiles["file:99"]) != 0 {
		t.Fatalf("cursor should clear deleted direct file, got %#v", next.ConnectorCursor)
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

func TestDoJSON_429HonorsRetryAfterAndRecovers(t *testing.T) {
	var apiHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/courses", func(w http.ResponseWriter, r *http.Request) {
		if apiHits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":"rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli, err := NewClient(&Config{
		BaseURL:      srv.URL,
		ClientID:     "cid",
		ClientSecret: "sec",
		AccessToken:  "tok",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	started := time.Now()
	body, err := cli.doJSON(context.Background(), http.MethodGet, "/api/v1/courses", nil)
	if err != nil {
		t.Fatalf("429 should recover after Retry-After: %v", err)
	}
	if string(body) != `[]` {
		t.Fatalf("unexpected recovered body: %s", body)
	}
	if got := apiHits.Load(); got != 2 {
		t.Fatalf("429 should be retried once, got %d requests", got)
	}
	if elapsed := time.Since(started); elapsed < minRateLimitBackoff {
		t.Fatalf("Retry-After=0 should still yield for at least %v, elapsed %v", minRateLimitBackoff, elapsed)
	}
}

func TestDoJSON_429StopsAfterBoundedRetries(t *testing.T) {
	var apiHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/courses", func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"rate limited"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli, err := NewClient(&Config{
		BaseURL:      srv.URL,
		ClientID:     "cid",
		ClientSecret: "sec",
		AccessToken:  "tok",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = cli.doJSON(context.Background(), http.MethodGet, "/api/v1/courses", nil)
	if err == nil || !strings.Contains(err.Error(), "status 429") {
		t.Fatalf("expected bounded 429 error, got %v", err)
	}
	if got := apiHits.Load(); got != int64(maxRateLimitRetries+1) {
		t.Fatalf("429 retry budget should allow %d requests, got %d", maxRateLimitRetries+1, got)
	}
}

func TestDoJSON_429WaitStopsWhenContextIsCanceled(t *testing.T) {
	var apiHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	cli, err := NewClient(&Config{
		BaseURL:      srv.URL,
		ClientID:     "cid",
		ClientSecret: "sec",
		AccessToken:  "tok",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = cli.doJSON(ctx, http.MethodGet, "/api/v1/courses", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("429 wait should stop with its context, got %v", err)
	}
	if got := apiHits.Load(); got != 1 {
		t.Fatalf("canceled wait must not retry, got %d requests", got)
	}
}

func TestNewClient_SameDataSourceSharesRateLimiter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)

	newClient := func(dataSourceID string) *Client {
		cli, err := NewClient(&Config{
			DataSourceID: dataSourceID,
			BaseURL:      srv.URL,
			ClientID:     "cid",
			ClientSecret: "sec",
			AccessToken:  "tok",
			RefreshToken: "refresh",
			ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		}, nil)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		return cli
	}

	clientA, clientB := newClient("ds-rate-shared"), newClient("ds-rate-shared")
	if clientA.limiter != clientB.limiter {
		t.Fatal("same DataSource clients must share a rate limiter")
	}
	clientC := newClient("ds-rate-other")
	if clientA.limiter == clientC.limiter {
		t.Fatal("different DataSources must not share a rate limiter")
	}
}

func TestClientFromConfig_UsesDistributedRateLimiter(t *testing.T) {
	var apiHits atomic.Int64
	var limiterHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":101,"name":"Course"}]`)
	}))
	t.Cleanup(srv.Close)

	config := &types.DataSourceConfig{
		Type: types.ConnectorTypeCanvas,
		Credentials: map[string]interface{}{
			"base_url":      srv.URL,
			"client_id":     "cid",
			"client_secret": "sec",
			"access_token":  "tok",
			"refresh_token": "refresh",
			"expires_at":    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		RuntimeDataSourceID: "ds-distributed-rate-hook",
		WaitForRateLimit: func(context.Context) error {
			limiterHits.Add(1)
			return nil
		},
	}

	resources, err := NewConnector().ListResources(context.Background(), config, "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 || apiHits.Load() != 1 || limiterHits.Load() != 1 {
		t.Fatalf("resources=%d api hits=%d limiter hits=%d, want 1/1/1", len(resources), apiHits.Load(), limiterHits.Load())
	}
}

func TestClientFromConfig_DistributedRateLimiterFailureStopsRequest(t *testing.T) {
	var apiHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)

	config := &types.DataSourceConfig{
		Type: types.ConnectorTypeCanvas,
		Credentials: map[string]interface{}{
			"base_url":      srv.URL,
			"client_id":     "cid",
			"client_secret": "sec",
			"access_token":  "tok",
			"refresh_token": "refresh",
			"expires_at":    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
		RuntimeDataSourceID: "ds-distributed-rate-error",
		WaitForRateLimit: func(context.Context) error {
			return errors.New("redis unavailable")
		},
	}

	_, err := NewConnector().ListResources(context.Background(), config, "")
	if err == nil || !strings.Contains(err.Error(), "canvas distributed rate limiter") {
		t.Fatalf("expected distributed rate limiter error, got %v", err)
	}
	if apiHits.Load() != 0 {
		t.Fatalf("upstream request must not run after limiter failure, got %d", apiHits.Load())
	}
}

func TestRefreshAccessToken_ConcurrentSingleflight(t *testing.T) {
	var refreshHits atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		once.Do(func() { close(started) })
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli, err := NewClient(&Config{
		BaseURL:      srv.URL,
		ClientID:     "cid",
		ClientSecret: "sec",
		AccessToken:  "old-tok",
		RefreshToken: "ref-1",
		ExpiresAt:    time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
	}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- cli.RefreshAccessToken(context.Background())
		}()
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for refresh to start")
	}
	// Give siblings time to join the singleflight before the leader finishes.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("RefreshAccessToken: %v", err)
		}
	}
	if got := refreshHits.Load(); got != 1 {
		t.Fatalf("refresh hits=%d want 1 (singleflight)", got)
	}
	if cli.cfg.AccessToken != "new-tok" {
		t.Fatalf("token not rotated, got %q", cli.cfg.AccessToken)
	}
}

func TestRefreshAccessToken_SameDataSourceSharesSingleflight(t *testing.T) {
	var refreshHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if refreshHits.Add(1) > 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"refresh token already rotated"}`)
			return
		}
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	newClient := func() *Client {
		cli, err := NewClient(&Config{
			DataSourceID: "ds-shared-refresh",
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
		return cli
	}
	clientA, clientB := newClient(), newClient()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, cli := range []*Client{clientA, clientB} {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			errs <- c.RefreshAccessToken(context.Background())
		}(cli)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("shared refresh should succeed for both clients: %v", err)
		}
	}
	if got := refreshHits.Load(); got != 1 {
		t.Fatalf("same data source should refresh once, got %d refreshes", got)
	}
	for name, client := range map[string]*Client{"A": clientA, "B": clientB} {
		if client.cfg.AccessToken != "new-tok" || client.cfg.RefreshToken != "ref-2" {
			t.Fatalf("client %s did not receive shared token: access=%q refresh=%q", name, client.cfg.AccessToken, client.cfg.RefreshToken)
		}
	}
}

// Characterization test: process-local singleflight cannot coordinate two app
// replicas. Under strict refresh-token rotation both processes submit the same
// old token; Canvas accepts one request and rejects the other.
func TestRefreshAccessToken_DifferentProcessesDoNotShareSingleflight(t *testing.T) {
	var refreshHits atomic.Int64
	var invalidRefreshHits atomic.Int64
	arrived := make(chan struct{})
	var arrivedOnce sync.Once
	currentRefresh := "old-refresh"
	var tokenMu sync.Mutex

	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if refreshHits.Add(1) == 2 {
			arrivedOnce.Do(func() { close(arrived) })
		}
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			http.Error(w, "timed out waiting for concurrent refresh", http.StatusGatewayTimeout)
			return
		}

		tokenMu.Lock()
		defer tokenMu.Unlock()
		if r.Form.Get("refresh_token") != currentRefresh {
			invalidRefreshHits.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"refresh token already rotated"}`)
			return
		}
		currentRefresh = "new-refresh"
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	for i := range commands {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCanvasRefreshHelperProcess$")
		cmd.Env = append(os.Environ(),
			"WEKNORA_CANVAS_REFRESH_HELPER=1",
			"WEKNORA_CANVAS_HELPER_BASE_URL="+srv.URL,
		)
		cmd.Stdout = &outputs[i]
		cmd.Stderr = &outputs[i]
		commands[i] = cmd
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
	}

	succeeded := 0
	failed := 0
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			failed++
			t.Logf("helper %d failed as expected: %s", i, strings.TrimSpace(outputs[i].String()))
		} else {
			succeeded++
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Fatalf("cross-process strict rotation: succeeded=%d failed=%d outputs=%q/%q",
			succeeded, failed, outputs[0].String(), outputs[1].String())
	}
	if refreshHits.Load() != 2 || invalidRefreshHits.Load() != 1 {
		t.Fatalf("refresh hits=%d invalid=%d, want 2/1", refreshHits.Load(), invalidRefreshHits.Load())
	}
}

func TestRefreshAccessToken_ExternalLockReloadAvoidsSecondRefresh(t *testing.T) {
	var refreshHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var distributedMu sync.Mutex
	var persistedMu sync.Mutex
	persisted := tokenState{AccessToken: "old-access", RefreshToken: "old-refresh"}

	newClient := func(dataSourceID string) *Client {
		cli, err := NewClient(&Config{
			BaseURL:      srv.URL,
			ClientID:     "cid",
			ClientSecret: "sec",
			DataSourceID: dataSourceID,
			AccessToken:  "old-access",
			RefreshToken: "old-refresh",
		}, func(_ context.Context, credentials map[string]interface{}) error {
			persistedMu.Lock()
			persisted = tokenState{
				AccessToken:  credentialString(credentials, "access_token"),
				RefreshToken: credentialString(credentials, "refresh_token"),
				ExpiresAt:    credentialString(credentials, "expires_at"),
			}
			persistedMu.Unlock()
			return nil
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		cli.acquireRefreshLock = func(context.Context) (func(), error) {
			distributedMu.Lock()
			return distributedMu.Unlock, nil
		}
		cli.onReload = func(context.Context) (map[string]interface{}, error) {
			persistedMu.Lock()
			defer persistedMu.Unlock()
			return map[string]interface{}{
				"access_token":  persisted.AccessToken,
				"refresh_token": persisted.RefreshToken,
				"expires_at":    persisted.ExpiresAt,
			}, nil
		}
		return cli
	}

	// Different IDs bypass the process-local singleflight/cache in this unit
	// test, modeling two separate app processes whose only shared coordinator is
	// the external lock plus persisted credential reload.
	clients := []*Client{newClient("process-a"), newClient("process-b")}
	errs := make(chan error, len(clients))
	var wg sync.WaitGroup
	for _, cli := range clients {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			errs <- func() error {
				source := c.currentTokenState()
				_, err := c.refreshAccessTokenCoordinated(context.Background(), source)
				return err
			}()
		}(cli)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("coordinated refresh failed: %v", err)
		}
	}
	if refreshHits.Load() != 1 {
		t.Fatalf("external coordinator should allow one Canvas refresh, got %d", refreshHits.Load())
	}
	for i, cli := range clients {
		if cli.cfg.AccessToken != "new-access" || cli.cfg.RefreshToken != "new-refresh" {
			t.Fatalf("client %d did not adopt persisted token: access=%q refresh=%q", i, cli.cfg.AccessToken, cli.cfg.RefreshToken)
		}
	}
}

func TestRefreshAccessToken_SameDataSourceReusesCompletedRefresh(t *testing.T) {
	var refreshHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if refreshHits.Add(1) > 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"refresh token already rotated"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	newClient := func() *Client {
		cli, err := NewClient(&Config{
			DataSourceID: "ds-sequential-refresh",
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
		return cli
	}
	clientA, clientB := newClient(), newClient()

	if err := clientA.RefreshAccessToken(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if err := clientB.RefreshAccessToken(context.Background()); err != nil {
		t.Fatalf("second refresh should reuse completed result: %v", err)
	}
	if got := refreshHits.Load(); got != 1 {
		t.Fatalf("completed refresh should be reused, got %d token requests", got)
	}
	if clientB.cfg.AccessToken != "new-tok" || clientB.cfg.RefreshToken != "ref-2" {
		t.Fatalf("stale client did not receive completed result: access=%q refresh=%q", clientB.cfg.AccessToken, clientB.cfg.RefreshToken)
	}
}

func TestRefreshAccessToken_DifferentDataSourcesDoNotShare(t *testing.T) {
	var refreshHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	newClient := func(dataSourceID string) *Client {
		cli, err := NewClient(&Config{
			DataSourceID: dataSourceID,
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
		return cli
	}

	if err := newClient("ds-a").RefreshAccessToken(context.Background()); err != nil {
		t.Fatalf("refresh ds-a: %v", err)
	}
	if err := newClient("ds-b").RefreshAccessToken(context.Background()); err != nil {
		t.Fatalf("refresh ds-b: %v", err)
	}
	if got := refreshHits.Load(); got != 2 {
		t.Fatalf("different data sources must not share refreshes, got %d token requests", got)
	}
}

func TestDoJSON_DifferentClientsSameDataSourceShareRefresh(t *testing.T) {
	var refreshHits atomic.Int64
	var apiHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if refreshHits.Add(1) > 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"refresh token already rotated"}`)
			return
		}
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	mux.HandleFunc("/api/v1/courses", func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.Header.Get("Authorization") != "Bearer new-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"expired"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	newClient := func() *Client {
		cli, err := NewClient(&Config{
			DataSourceID: "ds-json-shared-refresh",
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
		return cli
	}
	clientA, clientB := newClient(), newClient()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, client := range []*Client{clientA, clientB} {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			_, err := c.doJSON(context.Background(), http.MethodGet, "/api/v1/courses", nil)
			errs <- err
		}(client)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("same-data-source requests should both recover: %v", err)
		}
	}
	if got := refreshHits.Load(); got != 1 {
		t.Fatalf("same data source should make one refresh request, got %d", got)
	}
	if got := apiHits.Load(); got != 4 {
		t.Fatalf("each request should make one 401 and one retry, got %d API requests", got)
	}
}

func TestRefreshAccessToken_PersistenceFailureIsSurfacedAndRetriedFromCache(t *testing.T) {
	var persisted atomic.Int64
	var refreshHits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dataSourceID := "ds-persistence-retry"
	sharedDataSourceRefresh.cache.Delete(dataSourceID)
	t.Cleanup(func() { sharedDataSourceRefresh.cache.Delete(dataSourceID) })

	newClient := func() *Client {
		cli, err := NewClient(&Config{
			BaseURL:      srv.URL,
			ClientID:     "cid",
			ClientSecret: "sec",
			DataSourceID: dataSourceID,
			AccessToken:  "old-tok",
			RefreshToken: "ref-1",
			ExpiresAt:    time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		}, func(context.Context, map[string]interface{}) error {
			if persisted.Add(1) == 1 {
				return fmt.Errorf("database unavailable")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		return cli
	}

	first := newClient()
	if err := first.RefreshAccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("first refresh should surface persistence failure, got %v", err)
	}
	if first.cfg.AccessToken != "new-tok" || first.cfg.RefreshToken != "ref-2" {
		t.Fatalf("first client did not retain rotated token in memory: access=%q refresh=%q", first.cfg.AccessToken, first.cfg.RefreshToken)
	}

	second := newClient()
	if err := second.RefreshAccessToken(context.Background()); err != nil {
		t.Fatalf("retry should persist the cached token: %v", err)
	}
	if refreshHits.Load() != 1 {
		t.Fatalf("persistence retry must not call Canvas refresh again, hits=%d", refreshHits.Load())
	}
	if persisted.Load() != 2 {
		t.Fatalf("persistence callback count=%d want 2", persisted.Load())
	}
	if second.cfg.AccessToken != "new-tok" || second.cfg.RefreshToken != "ref-2" {
		t.Fatalf("second client did not receive cached token: access=%q refresh=%q", second.cfg.AccessToken, second.cfg.RefreshToken)
	}
}

func TestDoJSON_Concurrent401Singleflight(t *testing.T) {
	var refreshHits atomic.Int64
	var apiHits atomic.Int64
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var refreshOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		refreshHits.Add(1)
		refreshOnce.Do(func() { close(refreshStarted) })
		<-releaseRefresh
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-tok","refresh_token":"ref-2","expires_in":3600}`)
	})
	mux.HandleFunc("/api/v1/users/self", func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		if r.Header.Get("Authorization") != "Bearer new-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"errors":[{"message":"unauthorized"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":1}`)
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

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cli.doJSON(context.Background(), http.MethodGet, "/api/v1/users/self", nil)
			errs <- err
		}()
	}

	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for refresh to start")
	}
	time.Sleep(50 * time.Millisecond)
	close(releaseRefresh)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("doJSON: %v", err)
		}
	}
	if got := refreshHits.Load(); got != 1 {
		t.Fatalf("refresh hits=%d want 1 (singleflight)", got)
	}
	if cli.cfg.AccessToken != "new-tok" {
		t.Fatalf("token not rotated, got %q", cli.cfg.AccessToken)
	}
}
