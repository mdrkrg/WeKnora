package chatpipeline

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestCollectScopedTextChildIDs(t *testing.T) {
	parentMap := map[string]*types.Chunk{
		"parent-1": {ID: "parent-1", ChunkType: types.ChunkTypeParentText},
		"text-x":   {ID: "text-x", ChunkType: types.ChunkTypeText},
	}
	results := []*types.SearchResult{
		{ID: "text-1", ChunkType: string(types.ChunkTypeText), ParentChunkID: "parent-1"},
		{ID: "img-1", ChunkType: string(types.ChunkTypeImageOCR), ParentChunkID: "text-2"},
		{ID: "text-3", ChunkType: string(types.ChunkTypeText), ParentChunkID: "text-x"}, // not parent_text
	}
	ids := collectScopedTextChildIDs(results, parentMap)
	if len(ids) != 2 {
		t.Fatalf("ids: %v", ids)
	}
}

func TestAssignScopedImageInfo_FiltersToContentURLs(t *testing.T) {
	all, _ := json.Marshal([]types.ImageInfo{
		{URL: "u1", OCRText: "one"},
		{URL: "u2", OCRText: "two"},
	})
	r := &types.SearchResult{
		Content:   "![p2](u2)",
		ImageInfo: string(all),
	}
	assignScopedImageInfo(r, nil, "missing-child")
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(r.ImageInfo), &infos); err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].URL != "u2" {
		t.Fatalf("filtered: %+v", infos)
	}
}

func TestParentChildImageHit_WindowSliceAndFilter(t *testing.T) {
	parentContent := "![p1](u1)\n\n![p2](u2)\n\n![p3](u3)"
	textStart := len([]rune("![p1](u1)\n\n"))
	textEnd := textStart + len([]rune("![p2](u2)"))
	sliced := searchutil.SliceContentByDocumentRange(parentContent, 0, textStart, textEnd)
	if sliced != "![p2](u2)" {
		t.Fatalf("slice: got %q", sliced)
	}

	all, _ := json.Marshal([]types.ImageInfo{
		{URL: "u1"}, {URL: "u2"}, {URL: "u3"},
	})
	filtered := searchutil.FilterImageInfoByContentURLs(sliced, string(all))
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(filtered), &infos); err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].URL != "u2" {
		t.Fatalf("infos: %+v", infos)
	}
}

// stubChunkRepo implements ChunkRepository for resolveParentChunks tests.
// It embeds the interface so only the methods actually called need overrides;
// calling any other method panics (nil interface dereference).
type stubChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (r *stubChunkRepo) ListChunksByID(_ context.Context, _ uint64, ids []string) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range ids {
		if c, ok := r.chunks[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *stubChunkRepo) ListChunksByParentIDs(_ context.Context, _ uint64, parentIDs []string) ([]*types.Chunk, error) {
	return nil, nil
}

func TestResolveParentChunks_SetSegmentTextToParentContent(t *testing.T) {
	// parentContent: "## Section\n\n![img](u1)\n\nSome text here\n\n![img2](u2)\n\nMore text"
	// rune offsets (all ASCII, so runes == bytes):
	//   0-10  "## Section"
	//   10-12 "\n\n"
	//   12-23 "![img](u1)"
	//   23-25 "\n\n"
	//   25-39 "Some text here"
	//   39-41 "\n\n"
	//   41-53 "![img2](u2)"
	//   53-55 "\n\n"
	//   55-64 "More text"
	parentContent := "## Section\n\n![img](u1)\n\nSome text here\n\n![img2](u2)\n\nMore text"
	parentStart := 100
	parentEnd := parentStart + len([]rune(parentContent)) // 164

	parent := &types.Chunk{
		ID:        "parent-1",
		Content:   parentContent,
		StartAt:   parentStart,
		EndAt:     parentEnd,
		ChunkType: types.ChunkTypeParentText,
	}

	childContent := "Some text here"
	childStart := parentStart + 25 // offset of "Some text here" in parent
	childEnd := childStart + len([]rune(childContent))

	child := &types.SearchResult{
		ID:            "child-1",
		ChunkType:     string(types.ChunkTypeText),
		ParentChunkID: "parent-1",
		Content:       childContent,
		StartAt:       childStart,
		EndAt:         childEnd,
	}

	repo := &stubChunkRepo{chunks: map[string]*types.Chunk{"parent-1": parent}}
	plugin := &PluginMerge{chunkRepo: repo}

	chatManage := &types.ChatManage{}
	chatManage.TenantID = 1

	out := plugin.resolveParentChunks(context.Background(), chatManage, []*types.SearchResult{child})
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	result := out[0]

	if len(result.ContentSegments) != 1 {
		t.Fatalf("expected 1 content segment, got %d", len(result.ContentSegments))
	}
	seg := result.ContentSegments[0]

	if seg.Text != parent.Content {
		t.Errorf("segment.Text should be unpruned parent content\ngot:  %q (runeLen=%d)\nwant: %q (runeLen=%d)",
			seg.Text, len([]rune(seg.Text)), parent.Content, len([]rune(parent.Content)))
	}

	if seg.SourceStart != parent.StartAt {
		t.Errorf("segment.SourceStart = %d, want %d", seg.SourceStart, parent.StartAt)
	}
	if seg.SourceEnd != parent.EndAt {
		t.Errorf("segment.SourceEnd = %d, want %d", seg.SourceEnd, parent.EndAt)
	}
	if seg.ChunkID != parent.ID {
		t.Errorf("segment.ChunkID = %q, want %q", seg.ChunkID, parent.ID)
	}
	if seg.ChunkType != string(parent.ChunkType) {
		t.Errorf("segment.ChunkType = %q, want %q", seg.ChunkType, string(parent.ChunkType))
	}

	if result.Content == seg.Text {
		t.Errorf("result.Content (pruned) should differ from segment.Text (unpruned); both are %q", result.Content)
	}
}
