package chatpipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// A trusted parent chunk yields a parent segment (unpruned artifact slice)
// plus the child's own segment; content is the pruned parent joined with the
// child body.
func TestResolveParentChunksMaintainsSegments(t *testing.T) {
	parentContent := "## Section\n\n![img](u1)\n\nSome parent body\n\n![img2](u2)\n\nMore parent body"
	parentStart := 100
	parentEnd := parentStart + len([]rune(parentContent))

	parent := &types.Chunk{
		ID: "parent-1", KnowledgeID: "k1", ChunkType: types.ChunkTypeParentText,
		Content: parentContent, StartAt: parentStart, EndAt: parentEnd,
	}

	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "u2", OCRText: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{"parent-1": parent},
		children: map[string][]*types.Chunk{
			"child-1": {
				{
					ID: "img", ParentChunkID: "child-1", ChunkType: types.ChunkTypeImageOCR,
					ImageInfo: string(imageInfo),
				},
			},
		},
	}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	childContent := "child unique body"
	child := &types.SearchResult{
		ID: "child-1", KnowledgeID: "k1", ChunkType: string(types.ChunkTypeText),
		ParentChunkID: "parent-1", Content: childContent,
		StartAt: parentStart + 20, EndAt: parentStart + 20 + len([]rune(childContent)),
		ContentSegments: []types.ContentSegment{{
			Text: childContent, ChunkID: "child-1", KnowledgeID: "k1",
			SourceStart: parentStart + 20, SourceEnd: parentStart + 20 + len([]rune(childContent)),
			ChunkType: string(types.ChunkTypeText),
		}},
	}

	out := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{child})
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	r := out[0]

	wantParent := searchutil.PruneMarkdownImagesByImageInfo(parentContent, string(imageInfo))
	wantContent := searchutil.JoinChunkContent(wantParent, childContent, "\n\n")
	if r.Content != wantContent {
		t.Fatalf("content mismatch:\n  want %q\n  got  %q", wantContent, r.Content)
	}

	if len(r.ContentSegments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(r.ContentSegments))
	}
	parentSeg := r.ContentSegments[0]
	if parentSeg.Text != parentContent {
		t.Errorf("parent segment text = %q, want unpruned parent content", parentSeg.Text)
	}
	if parentSeg.ChunkID != "parent-1" || parentSeg.KnowledgeID != "k1" {
		t.Errorf("parent segment identity = %q/%q", parentSeg.ChunkID, parentSeg.KnowledgeID)
	}
	if parentSeg.SourceStart != parentStart || parentSeg.SourceEnd != parentEnd {
		t.Errorf("parent segment range = [%d,%d), want [%d,%d)", parentSeg.SourceStart, parentSeg.SourceEnd, parentStart, parentEnd)
	}
	if parentSeg.SourceEnd-parentSeg.SourceStart != len([]rune(parentSeg.Text)) {
		t.Error("parent segment violates exact-slice invariant")
	}

	childSeg := r.ContentSegments[1]
	if childSeg.ChunkID != "child-1" || childSeg.Text != childContent {
		t.Errorf("child segment = %+v, want child-1 exact slice", childSeg)
	}
	if childSeg.SourceEnd-childSeg.SourceStart != len([]rune(childSeg.Text)) {
		t.Error("child segment violates exact-slice invariant")
	}
}

// An edited parent chunk still joins its context into content, but the
// parent segment is omitted: only the child's exact segment remains.
func TestResolveParentChunksUntrustedParentOmitsParentSegment(t *testing.T) {
	parentContent := "## Section\n\nparent body text here"
	parent := &types.Chunk{
		ID: "parent-1", KnowledgeID: "k1", ChunkType: types.ChunkTypeParentText,
		Content: parentContent, StartAt: 0, EndAt: 5, // length-inconsistent: edited
		ContentRevision: 2,
	}
	repo := &expandChunkRepo{chunks: map[string]*types.Chunk{"parent-1": parent}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	childContent := "child unique body"
	child := &types.SearchResult{
		ID: "child-1", KnowledgeID: "k1", ChunkType: string(types.ChunkTypeText),
		ParentChunkID: "parent-1", Content: childContent, StartAt: 30, EndAt: 30 + len([]rune(childContent)),
		ContentSegments: []types.ContentSegment{{
			Text: childContent, ChunkID: "child-1", KnowledgeID: "k1",
			SourceStart: 30, SourceEnd: 30 + len([]rune(childContent)),
			ChunkType: string(types.ChunkTypeText),
		}},
	}

	out := plugin.resolveParentChunks(ctx, &types.ChatManage{}, []*types.SearchResult{child})
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	r := out[0]
	if !strings.Contains(r.Content, "parent body text here") {
		t.Fatalf("parent context must still join into content: %q", r.Content)
	}
	if len(r.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment (untrusted parent omitted), got %d", len(r.ContentSegments))
	}
	if r.ContentSegments[0].ChunkID != "child-1" {
		t.Errorf("segment chunk_id = %q, want child-1", r.ContentSegments[0].ChunkID)
	}
	if r.ContentSegments[0].SourceEnd-r.ContentSegments[0].SourceStart != len([]rune(r.ContentSegments[0].Text)) {
		t.Error("child segment violates exact-slice invariant")
	}
}
