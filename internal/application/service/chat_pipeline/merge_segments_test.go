package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestAddTrimmedSegments_SingleSegmentPartialTrim(t *testing.T) {
	src := &types.SearchResult{
		ContentSegments: []types.ContentSegment{
			{Text: "abcde", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 5, ChunkType: "text"},
		},
	}
	dst := &types.SearchResult{}
	var pm PluginMerge
	pm.addTrimmedSegments(dst, src, 2)

	if len(dst.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(dst.ContentSegments))
	}
	s := dst.ContentSegments[0]
	if s.Text != "cde" {
		t.Errorf("text = %q, want %q", s.Text, "cde")
	}
	if s.SourceStart != 2 {
		t.Errorf("source_start = %d, want 2", s.SourceStart)
	}
	if s.SourceEnd != 5 {
		t.Errorf("source_end = %d, want 5", s.SourceEnd)
	}
}

func TestCopySegments(t *testing.T) {
	src := &types.SearchResult{
		ContentSegments: []types.ContentSegment{
			{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
			{Text: "cd", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 4, ChunkType: "text"},
		},
	}
	dst := &types.SearchResult{
		ContentSegments: []types.ContentSegment{
			{Text: "base", ChunkID: "c0", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 4, ChunkType: "text"},
		},
	}
	var pm PluginMerge
	pm.copySegments(dst, src)

	if len(dst.ContentSegments) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(dst.ContentSegments))
	}
	if dst.ContentSegments[1].Text != "ab" || dst.ContentSegments[2].Text != "cd" {
		t.Errorf("copied segments = %q, %q", dst.ContentSegments[1].Text, dst.ContentSegments[2].Text)
	}
}

// mergeSequentialChunks merges a trusted overlapping pair: position overlap
// of 1 ("AAAA" / "AABB") is exactly verified and trimmed. The incoming
// chunk's segment must be trimmed by the actual overlap and re-anchored.
func TestMergeSequentialChunksExtendMaintainsSegments(t *testing.T) {
	base := &types.SearchResult{
		ID: "c1", Content: "AAAA", StartAt: 0, EndAt: 4, ChunkIndex: 0,
		ChunkType: "text",
		ContentSegments: []types.ContentSegment{
			{Text: "AAAA", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 4, ChunkType: "text"},
		},
	}
	next := &types.SearchResult{
		ID: "c2", Content: "AABB", StartAt: 3, EndAt: 7, ChunkIndex: 1,
		ChunkType: "text",
		ContentSegments: []types.ContentSegment{
			{Text: "AABB", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 3, SourceEnd: 7, ChunkType: "text"},
		},
	}

	var pm PluginMerge
	out := pm.mergeSequentialChunks(context.Background(), "k1", []*types.SearchResult{base, next})

	if len(out) != 1 {
		t.Fatalf("expected 1 merged result, got %d", len(out))
	}
	r := out[0]
	if r.Content != "AAAAABB" {
		t.Errorf("content = %q, want %q", r.Content, "AAAAABB")
	}
	if len(r.ContentSegments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(r.ContentSegments))
	}
	want := []types.ContentSegment{
		{Text: "AAAA", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 4, ChunkType: "text"},
		{Text: "ABB", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 4, SourceEnd: 7, ChunkType: "text"},
	}
	for i, w := range want {
		if r.ContentSegments[i] != w {
			t.Errorf("segment %d = %+v, want %+v", i, r.ContentSegments[i], w)
		}
	}
	if !containsID(r.SubChunkID, "c2") {
		t.Error("sub_chunk_id must include c2")
	}
}

// mergeSequentialChunks joins an untrusted pair via JoinChunkContent: the
// synthetic "\n\n" separator belongs to no segment and the incoming chunk's
// segments are appended in full.
func TestMergeSequentialChunksJoinMaintainsSegments(t *testing.T) {
	base := &types.SearchResult{
		ID: "c1", Content: "AAA", StartAt: 0, EndAt: 3, ChunkIndex: 0,
		ChunkType: "text",
		ContentSegments: []types.ContentSegment{
			{Text: "AAA", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 3, ChunkType: "text"},
		},
	}
	next := &types.SearchResult{
		ID: "c2", Content: "BBB", StartAt: 10, EndAt: 13, ChunkIndex: 1,
		ChunkType: "text", ContentRewritten: true,
		ContentSegments: []types.ContentSegment{
			{Text: "BBB", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 10, SourceEnd: 13, ChunkType: "text"},
		},
	}

	var pm PluginMerge
	out := pm.mergeSequentialChunks(context.Background(), "k1", []*types.SearchResult{base, next})

	if len(out) != 1 {
		t.Fatalf("expected 1 merged result, got %d", len(out))
	}
	r := out[0]
	if r.Content != "AAA\n\nBBB" {
		t.Errorf("content = %q, want %q", r.Content, "AAA\n\nBBB")
	}
	if len(r.ContentSegments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(r.ContentSegments))
	}
	if r.ContentSegments[1].Text != "BBB" || r.ContentSegments[1].SourceStart != 10 {
		t.Errorf("joined segment = %+v, want untrimmed BBB [10,13)", r.ContentSegments[1])
	}
}

// A fully covered chunk (contained text) produces no additional segments;
// its SubChunkID membership is still recorded.
func TestMergeSequentialChunksSubsumeAddsNoSegments(t *testing.T) {
	base := &types.SearchResult{
		ID: "c1", Content: "0123456789ABCDEF", StartAt: 0, EndAt: 16, ChunkIndex: 0,
		ChunkType: "text",
		ContentSegments: []types.ContentSegment{
			{Text: "0123456789ABCDEF", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 16, ChunkType: "text"},
		},
	}
	next := &types.SearchResult{
		ID: "c2", Content: "0123456789ABCD", StartAt: 2, EndAt: 16, ChunkIndex: 1,
		ChunkType: "text",
		ContentSegments: []types.ContentSegment{
			{Text: "0123456789ABCD", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 16, ChunkType: "text"},
		},
	}

	var pm PluginMerge
	out := pm.mergeSequentialChunks(context.Background(), "k1", []*types.SearchResult{base, next})

	if len(out) != 1 {
		t.Fatalf("expected 1 merged result, got %d", len(out))
	}
	r := out[0]
	if len(r.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment (covered chunk adds none), got %d", len(r.ContentSegments))
	}
	if !containsID(r.SubChunkID, "c2") {
		t.Error("sub_chunk_id must include the covered chunk c2")
	}
}

// A join that fully replaces the accumulated content (next contains acc)
// must swap the segment list to the incoming chunk's segments.
func TestMergeSequentialChunksJoinReplacesSegments(t *testing.T) {
	base := &types.SearchResult{
		ID: "c1", Content: "0123456789ABCDEF", StartAt: 0, EndAt: 16, ChunkIndex: 0,
		ChunkType: "text",
		ContentSegments: []types.ContentSegment{
			{Text: "0123456789ABCDEF", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 16, ChunkType: "text"},
		},
	}
	next := &types.SearchResult{
		ID: "c2", Content: "0123456789ABCDEFGHIJ", StartAt: 4, EndAt: 24, ChunkIndex: 1,
		ChunkType: "text", ContentRewritten: true,
		ContentSegments: []types.ContentSegment{
			{Text: "0123456789ABCDEFGHIJ", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 4, SourceEnd: 24, ChunkType: "text"},
		},
	}

	var pm PluginMerge
	out := pm.mergeSequentialChunks(context.Background(), "k1", []*types.SearchResult{base, next})

	if len(out) != 1 {
		t.Fatalf("expected 1 merged result, got %d", len(out))
	}
	r := out[0]
	if r.Content != "0123456789ABCDEFGHIJ" {
		t.Errorf("content = %q, want incoming content only", r.Content)
	}
	if len(r.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment (dst fully covered), got %d", len(r.ContentSegments))
	}
	if r.ContentSegments[0].ChunkID != "c2" {
		t.Errorf("segment chunk_id = %q, want c2", r.ContentSegments[0].ChunkID)
	}
}
