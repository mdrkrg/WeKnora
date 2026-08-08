package canvas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector implements datasource.Connector for Canvas LMS (OAuth2).
type Connector struct{}

// NewConnector creates a Canvas connector.
func NewConnector() *Connector {
	return &Connector{}
}

// Type returns the connector type identifier.
func (c *Connector) Type() string {
	return types.ConnectorTypeCanvas
}

// Validate checks credentials. Requires workspace Canvas OAuth app fields
// (injected by DataSourceService). Without an access_token it only verifies
// the app is present (pre-authorization). With a token it pings /users/self.
func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cfg, err := parseCanvasConfig(config, true)
	if err != nil {
		return err
	}
	if cfg.AccessToken == "" {
		return nil
	}
	cli, _, err := clientFromConfig(config)
	if err != nil {
		return err
	}
	return cli.Ping(ctx)
}

// ListResources implements lazy course → folder → file tree loading.
func (c *Connector) ListResources(
	ctx context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	cli, cfg, err := clientFromConfig(config)
	if err != nil {
		return nil, err
	}

	if parentID == "" {
		courses, err := cli.ListCourses(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]types.Resource, 0, len(courses))
		for _, course := range courses {
			name := course.Name
			if name == "" {
				name = course.CourseCode
			}
			out = append(out, types.Resource{
				ExternalID:  encodeCourseID(course.ID),
				Name:        name,
				Type:        resourceTypeCourse,
				URL:         fmt.Sprintf("%s/courses/%d", cfg.GetBaseURL(), course.ID),
				HasChildren: true,
			})
		}
		return out, nil
	}

	kind, id, err := parseResourceID(parentID)
	if err != nil {
		return nil, err
	}

	switch kind {
	case resourceTypeCourse:
		root, err := cli.GetCourseRootFolder(ctx, id)
		if err != nil {
			return nil, err
		}
		return c.listFolderChildren(ctx, cli, cfg, root.ID, parentID)
	case resourceTypeFolder:
		return c.listFolderChildren(ctx, cli, cfg, id, parentID)
	case resourceTypeFile:
		return []types.Resource{}, nil
	default:
		return nil, fmt.Errorf("unsupported parent resource %q", parentID)
	}
}

func (c *Connector) listFolderChildren(
	ctx context.Context, cli *Client, cfg *Config, folderID int64, parentExternalID string,
) ([]types.Resource, error) {
	folders, err := cli.ListFolders(ctx, folderID)
	if err != nil {
		return nil, err
	}
	files, err := cli.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}

	out := make([]types.Resource, 0, len(folders)+len(files))
	for _, f := range folders {
		out = append(out, types.Resource{
			ExternalID:  encodeFolderID(f.ID),
			Name:        f.Name,
			Type:        resourceTypeFolder,
			ParentID:    parentExternalID,
			URL:         fmt.Sprintf("%s/folders/%d", cfg.GetBaseURL(), f.ID),
			HasChildren: true,
		})
	}
	for _, f := range files {
		name := f.DisplayName
		if name == "" {
			name = f.Filename
		}
		updatedAt, _ := time.Parse(time.RFC3339, f.UpdatedAt)
		out = append(out, types.Resource{
			ExternalID:  encodeFileID(f.ID),
			Name:        name,
			Type:        resourceTypeFile,
			ParentID:    parentExternalID,
			URL:         fmt.Sprintf("%s/files/%d", cfg.GetBaseURL(), f.ID),
			ModifiedAt:  updatedAt,
			HasChildren: false,
		})
	}
	return out, nil
}

// ResolveResourceAncestors walks folder parents up to the owning course so the
// frontend can expand a lazily-loaded tree to reveal saved selections.
func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	cli, _, err := clientFromConfig(config)
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	var ancestors []string

	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ancestors = append(ancestors, id)
	}

	for _, rid := range resourceIDs {
		kind, id, err := parseResourceID(rid)
		if err != nil {
			continue
		}
		switch kind {
		case resourceTypeCourse:
			// top-level — nothing to expand above
		case resourceTypeFolder:
			c.walkFolderAncestors(ctx, cli, id, add)
		case resourceTypeFile:
			file, err := cli.GetFile(ctx, id)
			if err != nil {
				logger.Warnf(ctx, "[Canvas] resolve ancestors: get file %d: %v", id, err)
				continue
			}
			if file.FolderID != 0 {
				folder, err := cli.GetFolder(ctx, file.FolderID)
				if err != nil {
					logger.Warnf(ctx, "[Canvas] resolve ancestors: get folder %d: %v", file.FolderID, err)
					continue
				}
				if folder.ParentID == nil || *folder.ParentID == 0 {
					if strings.EqualFold(folder.ContextType, "course") && folder.ContextID != 0 {
						add(encodeCourseID(folder.ContextID))
					}
					continue
				}
				add(encodeFolderID(folder.ID))
				c.walkFolderAncestors(ctx, cli, file.FolderID, add)
			}
		}
	}
	return ancestors, nil
}

func (c *Connector) walkFolderAncestors(
	ctx context.Context, cli *Client, folderID int64, add func(string),
) {
	visited := map[int64]struct{}{}
	for folderID != 0 {
		if _, ok := visited[folderID]; ok {
			break
		}
		visited[folderID] = struct{}{}
		folder, err := cli.GetFolder(ctx, folderID)
		if err != nil {
			logger.Warnf(ctx, "[Canvas] resolve ancestors: get folder %d: %v", folderID, err)
			return
		}
		if folder.ParentID == nil || *folder.ParentID == 0 {
			if strings.EqualFold(folder.ContextType, "course") && folder.ContextID != 0 {
				add(encodeCourseID(folder.ContextID))
			}
			return
		}
		parentID := *folder.ParentID
		parent, err := cli.GetFolder(ctx, parentID)
		if err != nil {
			logger.Warnf(ctx, "[Canvas] resolve ancestors: get parent folder %d: %v", parentID, err)
			return
		}
		if parent.ParentID == nil || *parent.ParentID == 0 {
			if strings.EqualFold(parent.ContextType, "course") && parent.ContextID != 0 {
				add(encodeCourseID(parent.ContextID))
			}
			return
		}
		add(encodeFolderID(parent.ID))
		folderID = parentID
	}
}

// collectedFile is a file discovered during tree walk, optionally with list metadata.
type collectedFile struct {
	ID     int64
	Source string
	Meta   *canvasFile // from ListFiles; nil for directly selected file resources
}

// FetchAll downloads all files under the selected courses/folders/files.
func (c *Connector) FetchAll(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	items, _, _, err := c.fetch(ctx, config, resourceIDs, time.Time{})
	return items, err
}

// FetchIncremental downloads only files updated since the last sync cursor.
// Listing still walks the selected tree (Canvas has no folder-scoped "since" API),
// but downloads are skipped when ListFiles/GetFile updated_at is not after the cursor.
// Files present in the prior cursor but absent from the current listing are emitted
// as IsDeleted=true (deselected resources are not treated as deletions).
func (c *Connector) FetchIncremental(
	ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	since := time.Time{}
	if cursor != nil {
		since = cursor.LastSyncTime
	}
	prev := parseCanvasCursor(cursor)

	items, filesBySource, incomplete, err := c.fetch(ctx, config, config.ResourceIDs, since)
	if err != nil {
		var partial *datasource.PartialFetchError
		if !errors.As(err, &partial) {
			return nil, cursor, err
		}
	}

	if err == nil || isPartialFetch(err) {
		items = append(items, detectCanvasDeletions(prev, filesBySource, incomplete)...)
	}

	next := nextIncrementalCursor(cursor, prev, filesBySource, incomplete, err, time.Now().UTC())
	if err != nil {
		return items, next, err
	}
	return items, next, nil
}

func isPartialFetch(err error) bool {
	var partial *datasource.PartialFetchError
	return errors.As(err, &partial)
}

func parseCanvasCursor(cursor *types.SyncCursor) *canvasCursor {
	if cursor == nil || cursor.ConnectorCursor == nil {
		return nil
	}
	var prev canvasCursor
	b, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(b, &prev); err != nil {
		return nil
	}
	return &prev
}

// detectCanvasDeletions emits IsDeleted for files that previously belonged to a
// still-selected resource whose listing completed, but are missing from the
// current inventory. Deselected resources and incomplete listings are skipped.
func detectCanvasDeletions(
	prev *canvasCursor,
	current map[string]map[string]string,
	incomplete map[string]struct{},
) []types.FetchedItem {
	if prev == nil || len(prev.ResourceFiles) == 0 {
		return nil
	}
	var out []types.FetchedItem
	for src, prevFiles := range prev.ResourceFiles {
		if _, bad := incomplete[src]; bad {
			continue
		}
		currFiles, stillSelected := current[src]
		if !stillSelected {
			continue
		}
		for fileID := range prevFiles {
			if _, ok := currFiles[fileID]; ok {
				continue
			}
			out = append(out, types.FetchedItem{
				ExternalID:       fileID,
				IsDeleted:        true,
				SourceResourceID: src,
				Metadata: map[string]string{
					"channel": types.ChannelCanvas,
				},
			})
		}
	}
	return out
}

func (c *Connector) fetch(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string, since time.Time,
) ([]types.FetchedItem, map[string]map[string]string, map[string]struct{}, error) {
	cli, _, err := clientFromConfig(config)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(resourceIDs) == 0 {
		return nil, nil, nil, nil
	}

	files := map[int64]collectedFile{}
	filesBySource := make(map[string]map[string]string)
	incomplete := make(map[string]struct{})
	var warnings []string

	ensureSource := func(src string) {
		if _, ok := filesBySource[src]; !ok {
			filesBySource[src] = make(map[string]string)
		}
	}

	for _, rid := range resourceIDs {
		kind, id, err := parseResourceID(rid)
		if err != nil {
			warnings = append(warnings, err.Error())
			incomplete[rid] = struct{}{}
			continue
		}
		ensureSource(rid)
		switch kind {
		case resourceTypeFile:
			files[id] = collectedFile{ID: id, Source: rid}
		case resourceTypeFolder:
			if err := c.collectFilesUnderFolder(ctx, cli, id, rid, files); err != nil {
				warnings = append(warnings, err.Error())
				incomplete[rid] = struct{}{}
			}
		case resourceTypeCourse:
			root, err := cli.GetCourseRootFolder(ctx, id)
			if err != nil {
				warnings = append(warnings, err.Error())
				incomplete[rid] = struct{}{}
				continue
			}
			if err := c.collectFilesUnderFolder(ctx, cli, root.ID, rid, files); err != nil {
				warnings = append(warnings, err.Error())
				incomplete[rid] = struct{}{}
			}
		}
	}

	// Record listed files into the inventory before download filtering so
	// unchanged files still count as "present" for deletion detection.
	for _, cf := range files {
		if cf.Meta == nil {
			continue
		}
		ensureSource(cf.Source)
		filesBySource[cf.Source][encodeFileID(cf.ID)] = cf.Meta.UpdatedAt
	}

	items := make([]types.FetchedItem, 0, len(files))
	for _, cf := range files {
		meta := cf.Meta
		if meta == nil || meta.URL == "" {
			resolved, getErr := cli.GetFile(ctx, cf.ID)
			if getErr != nil {
				if errors.Is(getErr, datasource.ErrResourceNotFound) {
					// Directly selected file deleted at source — omit from inventory.
					continue
				}
				warnings = append(warnings, fmt.Sprintf("file %d: %v", cf.ID, getErr))
				incomplete[cf.Source] = struct{}{}
				continue
			}
			meta = resolved
			ensureSource(cf.Source)
			filesBySource[cf.Source][encodeFileID(cf.ID)] = meta.UpdatedAt
		}
		updatedAt, _ := time.Parse(time.RFC3339, meta.UpdatedAt)
		if !since.IsZero() && !updatedAt.IsZero() && !updatedAt.After(since) {
			items = append(items, types.FetchedItem{
				ExternalID:       encodeFileID(cf.ID),
				Title:            meta.DisplayName,
				FileName:         sanitizeFileName(meta.DisplayName),
				UpdatedAt:        updatedAt,
				SourceResourceID: cf.Source,
				IsSkipped:        true,
				Metadata: map[string]string{
					"channel": types.ChannelCanvas,
				},
			})
			continue
		}
		data, name, contentType, err := cli.DownloadFromMeta(ctx, meta)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("file %d: %v", cf.ID, err))
			continue
		}
		items = append(items, types.FetchedItem{
			ExternalID:       encodeFileID(cf.ID),
			Title:            name,
			Content:          data,
			ContentType:      contentType,
			FileName:         sanitizeFileName(name),
			URL:              "",
			UpdatedAt:        updatedAt,
			SourceResourceID: cf.Source,
			Metadata: map[string]string{
				"channel": types.ChannelCanvas,
			},
		})
	}

	if len(warnings) > 0 && len(items) == 0 {
		return nil, filesBySource, incomplete, fmt.Errorf("%w: %s", datasource.ErrFetchFailed, strings.Join(warnings, "; "))
	}
	if len(warnings) > 0 {
		return items, filesBySource, incomplete, &datasource.PartialFetchError{Details: warnings}
	}
	return items, filesBySource, incomplete, nil
}

func (c *Connector) collectFilesUnderFolder(
	ctx context.Context, cli *Client, folderID int64, source string, out map[int64]collectedFile,
) error {
	listed, err := cli.ListFiles(ctx, folderID)
	if err != nil {
		return err
	}
	for i := range listed {
		f := listed[i]
		out[f.ID] = collectedFile{ID: f.ID, Source: source, Meta: &f}
	}
	subs, err := cli.ListFolders(ctx, folderID)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if err := c.collectFilesUnderFolder(ctx, cli, sub.ID, source, out); err != nil {
			return err
		}
	}
	return nil
}

func nextIncrementalCursor(
	prevSync *types.SyncCursor,
	prev *canvasCursor,
	filesBySource map[string]map[string]string,
	incomplete map[string]struct{},
	fetchErr error,
	now time.Time,
) *types.SyncCursor {
	if fetchErr != nil && !isPartialFetch(fetchErr) {
		// Hard failure: keep the previous cursor entirely.
		return prevSync
	}

	// Keep LastSyncTime on partial download failures so omitted files remain
	// eligible for the next incremental run; still refresh the file inventory
	// so deletion detection can advance for successfully listed resources.
	lastSync := time.Time{}
	if prevSync != nil {
		lastSync = prevSync.LastSyncTime
	}
	if fetchErr == nil {
		lastSync = now
	}

	merged := mergeCanvasResourceFiles(prev, filesBySource, incomplete)
	cursorMap := make(map[string]interface{})
	b, err := json.Marshal(&canvasCursor{ResourceFiles: merged})
	if err == nil {
		_ = json.Unmarshal(b, &cursorMap)
	}
	return &types.SyncCursor{
		LastSyncTime:    lastSync,
		ConnectorCursor: cursorMap,
	}
}

// mergeCanvasResourceFiles builds the next inventory: complete current listings
// win; incomplete sources keep their previous entries; deselected sources drop.
func mergeCanvasResourceFiles(
	prev *canvasCursor,
	current map[string]map[string]string,
	incomplete map[string]struct{},
) map[string]map[string]string {
	out := make(map[string]map[string]string)
	for src, files := range current {
		if _, bad := incomplete[src]; bad {
			continue
		}
		cp := make(map[string]string, len(files))
		for k, v := range files {
			cp[k] = v
		}
		out[src] = cp
	}
	if prev != nil {
		for src, files := range prev.ResourceFiles {
			if _, bad := incomplete[src]; !bad {
				continue
			}
			if _, already := out[src]; already {
				continue
			}
			cp := make(map[string]string, len(files))
			for k, v := range files {
				cp[k] = v
			}
			out[src] = cp
		}
	}
	return out
}

func sanitizeFileName(name string) string {
	if name == "" {
		return "untitled"
	}
	base := path.Base(name)
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	result := replacer.Replace(base)
	const maxBytes = 200
	if len(result) > maxBytes {
		result = result[:maxBytes]
		for len(result) > 0 {
			r, size := utf8.DecodeLastRuneInString(result)
			if r != utf8.RuneError || size != 1 {
				break
			}
			result = result[:len(result)-1]
		}
	}
	return result
}

var _ datasource.Connector = (*Connector)(nil)
