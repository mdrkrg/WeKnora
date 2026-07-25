package chatpipeline

import (
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
	// overlap 3 exceeds first segment "ab" (2 runes), remaining 1 trims second segment "cde" -> "de"
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

func TestAddTrimmedSegments_FullOverlapSkipsAll(t *testing.T) {
	src := &types.SearchResult{
		ContentSegments: []types.ContentSegment{
			{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
			{Text: "cd", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 4, ChunkType: "text"},
		},
	}
	dst := &types.SearchResult{}
	var pm PluginMerge
	pm.addTrimmedSegments(dst, src, 4) // 4 == total text length

	if len(dst.ContentSegments) != 0 {
		t.Errorf("expected 0 segments when fully consumed, got %d", len(dst.ContentSegments))
	}
}
