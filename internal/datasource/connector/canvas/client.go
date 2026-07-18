package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultHTTPTimeout = 60 * time.Second
	tokenSkew          = 2 * time.Minute
	maxPageSize        = 100
	maxDownloadSize    = 100 << 20 // 100 MiB
)

// Client talks to the Canvas LMS REST + OAuth2 endpoints.
type Client struct {
	cfg        *Config
	httpClient *http.Client
	onUpdate   func(context.Context, map[string]interface{}) error

	mu        sync.Mutex
	refreshSF singleflight.Group
}

// NewClient builds a Canvas API client. onUpdate is optional and is invoked
// whenever access/refresh tokens are rotated so callers can persist them.
func NewClient(cfg *Config, onUpdate func(context.Context, map[string]interface{}) error) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("canvas config is nil")
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return &Client{
		cfg:        cfg,
		httpClient: datasource.NewConnectorHTTPClient(defaultHTTPTimeout),
		onUpdate:   onUpdate,
	}, nil
}

func clientFromConfig(config *types.DataSourceConfig) (*Client, *Config, error) {
	cfg, err := parseCanvasConfig(config, true)
	if err != nil {
		return nil, nil, err
	}
	var onUpdate func(context.Context, map[string]interface{}) error
	if config != nil && config.OnCredentialsUpdated != nil {
		onUpdate = config.OnCredentialsUpdated
	}
	cli, err := NewClient(cfg, onUpdate)
	if err != nil {
		return nil, nil, err
	}
	return cli, cfg, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         struct {
		ID int64 `json:"id"`
	} `json:"user"`
}

// ExchangeCode swaps an authorization code for tokens.
func (c *Client) ExchangeCode(ctx context.Context, code, redirectURI string) error {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("redirect_uri", redirectURI)
	form.Set("code", code)
	return c.applyTokenForm(ctx, form)
}

// RefreshAccessToken uses the refresh_token grant.
// Concurrent callers share one token exchange via singleflight so Canvas
// does not see redundant refresh_token rotations.
func (c *Client) RefreshAccessToken(ctx context.Context) error {
	_, err, _ := c.refreshSF.Do("refresh", func() (interface{}, error) {
		return nil, c.refreshAccessTokenOnce(ctx)
	})
	return err
}

func (c *Client) refreshAccessTokenOnce(ctx context.Context) error {
	c.mu.Lock()
	refresh := c.cfg.RefreshToken
	c.mu.Unlock()
	if refresh == "" {
		return fmt.Errorf("%w: canvas refresh_token missing; re-authorize required", datasource.ErrInvalidCredentials)
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("refresh_token", refresh)
	return c.applyTokenForm(ctx, form)
}

// refreshIfTokenUnchanged refreshes only when the in-memory access token is
// still the one that just received a 401. If another goroutine already
// rotated it, this is a no-op so the caller can retry with the new token.
func (c *Client) refreshIfTokenUnchanged(ctx context.Context, usedToken string) error {
	c.mu.Lock()
	current := c.cfg.AccessToken
	c.mu.Unlock()
	if current != usedToken {
		return nil
	}
	return c.RefreshAccessToken(ctx)
}

func (c *Client) applyTokenForm(ctx context.Context, form url.Values) error {
	tokenURL := c.cfg.GetBaseURL() + "/login/oauth2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("canvas token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: canvas token exchange status %d: %s", datasource.ErrInvalidCredentials, resp.StatusCode, string(body))
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return fmt.Errorf("decode canvas token: %w", err)
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("%w: canvas returned empty access_token", datasource.ErrInvalidCredentials)
	}

	c.mu.Lock()
	c.cfg.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.cfg.RefreshToken = tok.RefreshToken
	}
	if tok.ExpiresIn > 0 {
		c.cfg.ExpiresAt = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339)
	} else {
		c.cfg.ExpiresAt = ""
	}
	creds := c.cfg.TokenCredentialsMap()
	onUpdate := c.onUpdate
	c.mu.Unlock()

	if onUpdate != nil {
		if err := onUpdate(ctx, creds); err != nil {
			logger.Warnf(ctx, "[Canvas] failed to persist refreshed tokens: %v", err)
		}
	}
	return nil
}

func (c *Client) ensureValidToken(ctx context.Context) error {
	c.mu.Lock()
	token := c.cfg.AccessToken
	exp := c.cfg.TokenExpiry()
	refresh := c.cfg.RefreshToken
	c.mu.Unlock()

	if token == "" {
		return fmt.Errorf("%w: canvas access_token missing; complete OAuth authorization first", datasource.ErrInvalidCredentials)
	}
	if exp.IsZero() || !time.Now().Add(tokenSkew).After(exp) {
		return nil
	}
	if refresh == "" {
		return fmt.Errorf("%w: canvas access_token expired and no refresh_token", datasource.ErrInvalidCredentials)
	}
	return c.RefreshAccessToken(ctx)
}

// Ping verifies the access token via /api/v1/users/self.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.ensureValidToken(ctx); err != nil {
		return err
	}
	_, err := c.doJSON(ctx, http.MethodGet, "/api/v1/users/self", nil)
	return err
}

type canvasCourse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CourseCode string `json:"course_code"`
	Workflow   string `json:"workflow_state"`
}

type canvasFolder struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ParentID    *int64 `json:"parent_folder_id"`
	FullName    string `json:"full_name"`
	FilesURL    string `json:"files_url"`
	FoldersURL  string `json:"folders_url"`
	ContextType string `json:"context_type"`
	ContextID   int64  `json:"context_id"`
}

type canvasFile struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Filename    string `json:"filename"`
	ContentType string `json:"content-type"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	UpdatedAt   string `json:"updated_at"`
	FolderID    int64  `json:"folder_id"`
}

// ListCourses returns courses visible to the authorized user.
func (c *Client) ListCourses(ctx context.Context) ([]canvasCourse, error) {
	var out []canvasCourse
	err := c.paginate(ctx, "/api/v1/courses?per_page="+strconv.Itoa(maxPageSize)+"&enrollment_state=active", &out)
	return out, err
}

// GetCourseRootFolder returns the course files root folder.
func (c *Client) GetCourseRootFolder(ctx context.Context, courseID int64) (*canvasFolder, error) {
	body, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/courses/%d/folders/root", courseID), nil)
	if err != nil {
		return nil, err
	}
	var folder canvasFolder
	if err := json.Unmarshal(body, &folder); err != nil {
		return nil, err
	}
	return &folder, nil
}

// ListFolders lists immediate child folders of a folder.
func (c *Client) ListFolders(ctx context.Context, folderID int64) ([]canvasFolder, error) {
	var out []canvasFolder
	path := fmt.Sprintf("/api/v1/folders/%d/folders?per_page=%d", folderID, maxPageSize)
	err := c.paginate(ctx, path, &out)
	return out, err
}

// ListFiles lists immediate files in a folder.
func (c *Client) ListFiles(ctx context.Context, folderID int64) ([]canvasFile, error) {
	var out []canvasFile
	path := fmt.Sprintf("/api/v1/folders/%d/files?per_page=%d", folderID, maxPageSize)
	err := c.paginate(ctx, path, &out)
	return out, err
}

// GetFolder fetches folder metadata.
func (c *Client) GetFolder(ctx context.Context, folderID int64) (*canvasFolder, error) {
	body, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/folders/%d", folderID), nil)
	if err != nil {
		return nil, err
	}
	var folder canvasFolder
	if err := json.Unmarshal(body, &folder); err != nil {
		return nil, err
	}
	return &folder, nil
}

// GetFile fetches file metadata.
func (c *Client) GetFile(ctx context.Context, fileID int64) (*canvasFile, error) {
	body, err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v1/files/%d", fileID), nil)
	if err != nil {
		return nil, err
	}
	var file canvasFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

// DownloadFile fetches file metadata then downloads bytes with Bearer auth.
func (c *Client) DownloadFile(ctx context.Context, fileID int64) ([]byte, string, string, error) {
	meta, err := c.GetFile(ctx, fileID)
	if err != nil {
		return nil, "", "", err
	}
	return c.DownloadFromMeta(ctx, meta)
}

// DownloadFromMeta downloads bytes using an already-fetched file metadata record,
// avoiding a redundant GetFile round-trip. On 401 it refreshes the OAuth token
// once and retries the same download URL (Canvas requires Bearer auth; verifier
// query params are legacy and being removed).
func (c *Client) DownloadFromMeta(ctx context.Context, meta *canvasFile) ([]byte, string, string, error) {
	if meta == nil {
		return nil, "", "", fmt.Errorf("canvas file meta is nil")
	}
	if meta.URL == "" {
		return nil, "", "", fmt.Errorf("canvas file %d has empty download url", meta.ID)
	}
	if err := datasource.ValidateConnectorBaseURL(meta.URL); err != nil {
		// Download URLs may be on the same host with query params; validate host via parsed URL.
		u, parseErr := url.Parse(meta.URL)
		if parseErr != nil {
			return nil, "", "", err
		}
		if err2 := datasource.ValidateConnectorBaseURL(u.Scheme + "://" + u.Host); err2 != nil {
			return nil, "", "", err2
		}
	}
	return c.downloadFromMetaRetry(ctx, meta, true)
}

func (c *Client) downloadFromMetaRetry(ctx context.Context, meta *canvasFile, allowRefresh bool) ([]byte, string, string, error) {
	if err := c.ensureValidToken(ctx); err != nil {
		return nil, "", "", err
	}
	c.mu.Lock()
	token := c.cfg.AccessToken
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		data, err := readWithLimit(resp.Body, maxDownloadSize)
		if err != nil {
			return nil, "", "", fmt.Errorf("download file %d: %w", meta.ID, err)
		}
		name := meta.DisplayName
		if name == "" {
			name = meta.Filename
		}
		ct := meta.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		return data, name, ct, nil
	case resp.StatusCode == http.StatusUnauthorized:
		if allowRefresh {
			if refreshErr := c.refreshIfTokenUnchanged(ctx, token); refreshErr == nil {
				return c.downloadFromMetaRetry(ctx, meta, false)
			}
		}
		return nil, "", "", fmt.Errorf("%w: download file %d: status %d", datasource.ErrInvalidCredentials, meta.ID, resp.StatusCode)
	default:
		return nil, "", "", fmt.Errorf("download file %d: status %d", meta.ID, resp.StatusCode)
	}
}

func readWithLimit(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d-byte download limit", limit)
	}
	return data, nil
}

func (c *Client) paginate(ctx context.Context, firstPath string, dest interface{}) error {
	path := firstPath
	// dest must be pointer to slice of the element type — we accumulate via raw JSON arrays.
	var collected []json.RawMessage
	for path != "" {
		body, link, err := c.doJSONWithLink(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var page []json.RawMessage
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("decode canvas page: %w", err)
		}
		collected = append(collected, page...)
		path = parseNextLink(link)
	}
	wrapped, err := json.Marshal(collected)
	if err != nil {
		return err
	}
	return json.Unmarshal(wrapped, dest)
}

func parseNextLink(linkHeader string) string {
	// Link: <url>; rel="next", <url>; rel="last"
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			next := part[start+1 : end]
			// Convert absolute URL to path+query for doJSON.
			if u, err := url.Parse(next); err == nil {
				if u.Path != "" {
					q := u.RawQuery
					if q != "" {
						return u.Path + "?" + q
					}
					return u.Path
				}
			}
			return next
		}
	}
	return ""
}

func (c *Client) doJSON(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	b, _, err := c.doJSONWithLink(ctx, method, path, body)
	return b, err
}

func (c *Client) doJSONWithLink(ctx context.Context, method, path string, body io.Reader) ([]byte, string, error) {
	return c.doJSONWithLinkRetry(ctx, method, path, body, true)
}

func (c *Client) doJSONWithLinkRetry(ctx context.Context, method, path string, body io.Reader, allowRefresh bool) ([]byte, string, error) {
	if err := c.ensureValidToken(ctx); err != nil {
		return nil, "", err
	}
	c.mu.Lock()
	token := c.cfg.AccessToken
	base := c.cfg.GetBaseURL()
	c.mu.Unlock()

	full := path
	if !(strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")) {
		full = base + path
	}

	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", datasource.ErrFetchFailed, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return respBody, resp.Header.Get("Link"), nil
	case resp.StatusCode == 401:
		if allowRefresh {
			if refreshErr := c.refreshIfTokenUnchanged(ctx, token); refreshErr == nil {
				return c.doJSONWithLinkRetry(ctx, method, path, body, false)
			}
		}
		return nil, "", fmt.Errorf("%w: %s", datasource.ErrInvalidCredentials, string(respBody))
	case resp.StatusCode == 403:
		return nil, "", fmt.Errorf("%w: %s", datasource.ErrInvalidCredentials, string(respBody))
	case resp.StatusCode == 404:
		return nil, "", fmt.Errorf("%w: %s", datasource.ErrResourceNotFound, path)
	default:
		return nil, "", fmt.Errorf("%w: status %d: %s", datasource.ErrFetchFailed, resp.StatusCode, string(respBody))
	}
}

// BuildAuthorizeURL constructs the Canvas OAuth2 authorization URL.
func BuildAuthorizeURL(baseURL, clientID, redirectURI, state string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	return base + "/login/oauth2/auth?" + q.Encode()
}
