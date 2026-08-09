package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// baseSegment builds the initial segment a search result carries when
// created.
func baseSegment(chunk *types.Chunk) []types.ContentSegment {
	return []types.ContentSegment{{
		Text:        chunk.Content,
		ChunkID:     chunk.ID,
		KnowledgeID: chunk.KnowledgeID,
		SourceStart: chunk.StartAt,
		SourceEnd:   chunk.EndAt,
		ChunkType:   string(chunk.ChunkType),
	}}
}

func expandFixture(prev, base, next *types.Chunk) (*PluginMerge, context.Context, *types.SearchResult) {
	repo := &expandChunkRepo{chunks: map[string]*types.Chunk{
		prev.ID: prev,
		base.ID: base,
		next.ID: next,
	}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result := &types.SearchResult{
		ID:              base.ID,
		KnowledgeID:     base.KnowledgeID,
		ChunkType:       string(types.ChunkTypeText),
		Content:         base.Content,
		ContentSegments: baseSegment(base),
	}
	return plugin, ctx, result
}

// assertSegmentsExact verifies the exact-slice invariant per segment and
// that the segments cover content when synthetic "\n\n" separators are
// inserted between adjacent segments (the full-join layout these fixtures
// produce).
func assertSegmentsExact(t *testing.T, content string, segs []types.ContentSegment) {
	t.Helper()
	concat := ""
	for i, seg := range segs {
		if i > 0 {
			concat += "\n\n"
		}
		if seg.SourceEnd-seg.SourceStart != len([]rune(seg.Text)) {
			t.Errorf("exact-slice invariant violated for %q: range %d != runeLen %d",
				seg.Text, seg.SourceEnd-seg.SourceStart, len([]rune(seg.Text)))
		}
		concat += seg.Text
	}
	if concat != content {
		t.Errorf("segments do not cover content:\n  segments=%q\n  content =%q", concat, content)
	}
}

// A short base chunk expanded with trusted neighbors carries one exact-slice
// segment per chunk; synthetic "\n\n" separators belong to no segment.
func TestExpandShortContextMaintainsSegments(t *testing.T) {
	prev := &types.Chunk{
		ID: "prev", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("P", 150), StartAt: 0, EndAt: 150, NextChunkID: "base",
	}
	base := &types.Chunk{
		ID: "base", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("B", 60), StartAt: 150, EndAt: 210, PreChunkID: "prev", NextChunkID: "next",
	}
	next := &types.Chunk{
		ID: "next", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("N", 150), StartAt: 210, EndAt: 360, PreChunkID: "base",
	}

	plugin, ctx, result := expandFixture(prev, base, next)
	out := plugin.expandShortContextWithNeighbors(ctx, &types.ChatManage{}, []*types.SearchResult{result})

	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	r := out[0]
	wantContent := strings.Repeat("P", 150) + "\n\n" + strings.Repeat("B", 60) + "\n\n" + strings.Repeat("N", 150)
	if r.Content != wantContent {
		t.Fatalf("content mismatch:\n  want %q\n  got  %q", wantContent, r.Content)
	}
	if len(r.ContentSegments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(r.ContentSegments))
	}
	want := []types.ContentSegment{
		{
			Text: strings.Repeat("P", 150), ChunkID: "prev", KnowledgeID: "doc",
			SourceStart: 0, SourceEnd: 150, ChunkType: "text",
		},
		{
			Text: strings.Repeat("B", 60), ChunkID: "base", KnowledgeID: "doc",
			SourceStart: 150, SourceEnd: 210, ChunkType: "text",
		},
		{
			Text: strings.Repeat("N", 150), ChunkID: "next", KnowledgeID: "doc",
			SourceStart: 210, SourceEnd: 360, ChunkType: "text",
		},
	}
	for i, w := range want {
		if r.ContentSegments[i] != w {
			t.Errorf("segment %d = %+v, want %+v", i, r.ContentSegments[i], w)
		}
	}
	assertSegmentsExact(t, r.Content, r.ContentSegments)
}

// Truncation to maxLen cuts inside the trailing chunk: the last segment is
// trimmed and its source_end adjusted, keeping the exact-slice invariant.
func TestExpandShortContextTruncationTrimsLastSegment(t *testing.T) {
	prev := &types.Chunk{
		ID: "prev", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("P", 500), StartAt: 0, EndAt: 500, NextChunkID: "base",
	}
	base := &types.Chunk{
		ID: "base", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("B", 60), StartAt: 500, EndAt: 560, PreChunkID: "prev", NextChunkID: "next",
	}
	next := &types.Chunk{
		ID: "next", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("N", 500), StartAt: 560, EndAt: 1060, PreChunkID: "base",
	}

	plugin, ctx, result := expandFixture(prev, base, next)
	out := plugin.expandShortContextWithNeighbors(ctx, &types.ChatManage{}, []*types.SearchResult{result})

	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	r := out[0]
	if len(r.Content) != 850 {
		t.Fatalf("content length = %d, want 850 (maxLen)", len(r.Content))
	}
	if len(r.ContentSegments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(r.ContentSegments))
	}
	last := r.ContentSegments[2]
	if last.ChunkID != "next" {
		t.Fatalf("last segment chunk_id = %q, want next", last.ChunkID)
	}
	if runeLen(last.Text) != 286 {
		t.Errorf("last segment text length = %d, want 286", runeLen(last.Text))
	}
	if last.SourceEnd-last.SourceStart != runeLen(last.Text) {
		t.Errorf("last segment range %d != runeLen %d", last.SourceEnd-last.SourceStart, runeLen(last.Text))
	}
	assertSegmentsExact(t, r.Content, r.ContentSegments)
}

// An edited (range-untrusted) neighbor chunk still joins into content but its
// segment explicitly degrades to a [0,0) snapshot marker.
func TestExpandShortContextUntrustedNeighborDegradesToSnapshot(t *testing.T) {
	prev := &types.Chunk{
		ID: "prev", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("P", 150), StartAt: 0, EndAt: 150, NextChunkID: "base",
		ContentRevision: 2,
	}
	base := &types.Chunk{
		ID: "base", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("B", 60), StartAt: 150, EndAt: 210, PreChunkID: "prev", NextChunkID: "next",
	}
	next := &types.Chunk{
		ID: "next", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("N", 150), StartAt: 210, EndAt: 360, PreChunkID: "base",
	}

	plugin, ctx, result := expandFixture(prev, base, next)
	out := plugin.expandShortContextWithNeighbors(ctx, &types.ChatManage{}, []*types.SearchResult{result})

	r := out[0]
	if len(r.ContentSegments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(r.ContentSegments))
	}
	prevSeg := r.ContentSegments[0]
	if prevSeg.SourceStart != 0 || prevSeg.SourceEnd != 0 {
		t.Errorf("edited neighbor must degrade to [0,0), got [%d,%d)", prevSeg.SourceStart, prevSeg.SourceEnd)
	}
	if prevSeg.Text == "" {
		t.Error("snapshot segment text must be non-empty")
	}
	if prevSeg.ChunkID != "prev" {
		t.Errorf("chunk_id = %q, want prev", prevSeg.ChunkID)
	}
	// The base and next segments keep their exact ranges.
	for _, seg := range r.ContentSegments[1:] {
		if seg.SourceEnd-seg.SourceStart != len([]rune(seg.Text)) {
			t.Errorf("segment %+v violates exact-slice invariant", seg)
		}
	}
}

// A neighbor fully contained by the accumulated content is dropped by
// JoinChunkContent; its segment must be dropped as well.
func TestExpandShortContextContainedNeighborDropsSegment(t *testing.T) {
	prev := &types.Chunk{
		ID: "prev", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("P", 300), StartAt: 0, EndAt: 300, NextChunkID: "base",
	}
	base := &types.Chunk{
		ID: "base", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("B", 100), StartAt: 300, EndAt: 400, PreChunkID: "prev", NextChunkID: "next",
	}
	next := &types.Chunk{
		ID: "next", KnowledgeID: "doc", ChunkType: types.ChunkTypeText,
		Content: strings.Repeat("B", 100), StartAt: 400, EndAt: 500, PreChunkID: "base",
	}

	plugin, ctx, result := expandFixture(prev, base, next)
	out := plugin.expandShortContextWithNeighbors(ctx, &types.ChatManage{}, []*types.SearchResult{result})

	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	r := out[0]
	wantContent := strings.Repeat("P", 300) + "\n\n" + strings.Repeat("B", 100)
	if r.Content != wantContent {
		t.Fatalf("contained neighbor must be dropped:\n  want %q\n  got  %q", wantContent, r.Content)
	}
	if len(r.ContentSegments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(r.ContentSegments))
	}
	if r.ContentSegments[1].ChunkID != "base" {
		t.Errorf("second segment chunk_id = %q, want base", r.ContentSegments[1].ChunkID)
	}
	assertSegmentsExact(t, r.Content, r.ContentSegments)
}
