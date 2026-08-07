package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestChunk2SearchResultTrustedChunkSegment(t *testing.T) {
	chunk := &types.Chunk{
		ID:          "entity-1",
		KnowledgeID: "knowledge-1",
		Content:     "实体说明文本",
		StartAt:     30,
		EndAt:       36,
		ChunkType:   types.ChunkTypeEntity,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		Title:           "实体知识",
		FileName:        "entities.md",
		Source:          "file",
		Channel:         "api",
		KnowledgeBaseID: "kb-1",
	}

	res := chunk2SearchResult(chunk, knowledge)

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
	if seg.SourceStart != 30 || seg.SourceEnd != 36 {
		t.Errorf("source range = [%d,%d), want [30,36)", seg.SourceStart, seg.SourceEnd)
	}
	if seg.SourceEnd-seg.SourceStart != len([]rune(seg.Text)) {
		t.Errorf("exact-slice invariant violated: range %d != runeLen(text) %d",
			seg.SourceEnd-seg.SourceStart, len([]rune(seg.Text)))
	}
	if seg.ChunkType != string(types.ChunkTypeEntity) {
		t.Errorf("chunk_type = %q, want %q", seg.ChunkType, types.ChunkTypeEntity)
	}
}

func TestChunk2SearchResultEditedChunkDegradesToSnapshot(t *testing.T) {
	chunk := &types.Chunk{
		ID:              "entity-2",
		KnowledgeID:     "knowledge-1",
		Content:         "被编辑后的内容",
		StartAt:         30,
		EndAt:           36,
		ContentRevision: 3,
		ChunkType:       types.ChunkTypeEntity,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		Title:           "实体知识",
		FileName:        "entities.md",
		Source:          "file",
		Channel:         "api",
		KnowledgeBaseID: "kb-1",
	}

	res := chunk2SearchResult(chunk, knowledge)

	if len(res.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(res.ContentSegments))
	}
	seg := res.ContentSegments[0]
	if seg.SourceStart != 0 || seg.SourceEnd != 0 {
		t.Errorf("edited chunk must degrade to [0,0), got [%d,%d)", seg.SourceStart, seg.SourceEnd)
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
