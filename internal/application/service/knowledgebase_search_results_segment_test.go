package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildSearchResultContentSegment(t *testing.T) {
	s := &knowledgeBaseService{}

	chunk := &types.Chunk{
		ID:          "chunk-1",
		KnowledgeID: "knowledge-1",
		Content:     "登录模块使用说明",
		StartAt:     120,
		EndAt:       128,
		ChunkType:   types.ChunkTypeText,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		Title:           "登录模块文档",
		FileName:        "login.md",
		KnowledgeBaseID: "kb-1",
	}

	res := s.buildSearchResult(chunk, knowledge, 0.95, types.MatchTypeEmbedding, "登录模块")

	if len(res.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(res.ContentSegments))
	}
	seg := res.ContentSegments[0]
	if seg.Text != chunk.Content {
		t.Errorf("text = %q, want %q", seg.Text, chunk.Content)
	}
	if seg.ChunkID != chunk.ID || seg.KnowledgeID != chunk.KnowledgeID {
		t.Errorf("identity = %q/%q, want %q/%q", seg.ChunkID, seg.KnowledgeID, chunk.ID, chunk.KnowledgeID)
	}
	if seg.SourceStart != 120 || seg.SourceEnd != 128 {
		t.Errorf("source range = [%d,%d), want [120,128)", seg.SourceStart, seg.SourceEnd)
	}
	if seg.SourceEnd-seg.SourceStart != len([]rune(seg.Text)) {
		t.Errorf("exact-slice invariant violated: range %d != runeLen(text) %d",
			seg.SourceEnd-seg.SourceStart, len([]rune(seg.Text)))
	}
	if seg.ChunkType != string(types.ChunkTypeText) {
		t.Errorf("chunk_type = %q, want %q", seg.ChunkType, types.ChunkTypeText)
	}
}

func TestBuildSearchResultRangeInconsistentChunkDegradesToSnapshot(t *testing.T) {
	s := &knowledgeBaseService{}

	// Range length (2) does not match rune length of content (8).
	chunk := &types.Chunk{
		ID:          "chunk-2",
		KnowledgeID: "knowledge-1",
		Content:     "长度与范围不一致",
		StartAt:     0,
		EndAt:       2,
		ChunkType:   types.ChunkTypeText,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		Title:           "文档",
		FileName:        "doc.md",
		KnowledgeBaseID: "kb-1",
	}

	res := s.buildSearchResult(chunk, knowledge, 0.8, types.MatchTypeKeywords, "")

	if len(res.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(res.ContentSegments))
	}
	seg := res.ContentSegments[0]
	if seg.SourceStart != 0 || seg.SourceEnd != 0 {
		t.Errorf("range-inconsistent chunk must degrade to [0,0), got [%d,%d)", seg.SourceStart, seg.SourceEnd)
	}
	if seg.Text == "" {
		t.Error("snapshot segment text must be non-empty")
	}
	if seg.Text != chunk.Content {
		t.Errorf("text = %q, want %q", seg.Text, chunk.Content)
	}
	if seg.ChunkID != chunk.ID {
		t.Errorf("chunk_id = %q, want %q", seg.ChunkID, chunk.ID)
	}
}
