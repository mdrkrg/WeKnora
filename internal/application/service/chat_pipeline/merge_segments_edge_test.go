package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Overlap exceeding the first segment's length must consume runes across
// all segments, trimming the second and adjusting its source_start.
func TestAddTrimmedSegments_MultiSegmentOverlapExceedsFirst(t *testing.T) {
	src := &types.SearchResult{
		ContentSegments: []types.ContentSegment{
			{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
			{Text: "cde", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 5, ChunkType: "text"},
		},
	}
	dst := &types.SearchResult{}
	var pm PluginMerge
	pm.addTrimmedSegments(dst, src, 3)

	if len(dst.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment after overlap consumed first, got %d", len(dst.ContentSegments))
	}
	s := dst.ContentSegments[0]
	if s.Text != "de" {
		t.Errorf("text = %q, want %q", s.Text, "de")
	}
	if s.SourceStart != 3 {
		t.Errorf("source_start = %d, want 3", s.SourceStart)
	}
	if s.ChunkID != "c2" {
		t.Errorf("chunk_id = %q, want c2", s.ChunkID)
	}
}

// Overlap consuming the entire text leaves no segments: the covered chunk
// must not contribute any segment.
func TestAddTrimmedSegments_FullOverlapSkipsAll(t *testing.T) {
	src := &types.SearchResult{
		ContentSegments: []types.ContentSegment{
			{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
			{Text: "cd", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 4, ChunkType: "text"},
		},
	}
	dst := &types.SearchResult{}
	var pm PluginMerge
	pm.addTrimmedSegments(dst, src, 4)

	if len(dst.ContentSegments) != 0 {
		t.Errorf("expected 0 segments when fully consumed, got %d", len(dst.ContentSegments))
	}
}
