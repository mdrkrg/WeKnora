package searchutil

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestChunkRangeTrusted(t *testing.T) {
	tests := []struct {
		name  string
		chunk *types.Chunk
		want  bool
	}{
		{"nil chunk", nil, false},
		{"empty range", &types.Chunk{Content: "abc", StartAt: 5, EndAt: 5}, false},
		{"reversed range", &types.Chunk{Content: "abc", StartAt: 6, EndAt: 5}, false},
		{"edited chunk", &types.Chunk{Content: "abc", StartAt: 0, EndAt: 3, ContentRevision: 1}, false},
		{"length mismatch", &types.Chunk{Content: "abcd", StartAt: 0, EndAt: 3}, false},
		{"trusted ascii", &types.Chunk{Content: "abc", StartAt: 10, EndAt: 13}, true},
		{"trusted unicode", &types.Chunk{Content: "你好，世界", StartAt: 0, EndAt: 5}, true},
		{"rune count not byte count", &types.Chunk{Content: "你好", StartAt: 0, EndAt: 2}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChunkRangeTrusted(tt.chunk); got != tt.want {
				t.Errorf("ChunkRangeTrusted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSegmentForChunkTrusted(t *testing.T) {
	chunk := &types.Chunk{
		ID:          "chunk-1",
		KnowledgeID: "knowledge-1",
		Content:     "登录认证失败处理指南",
		StartAt:     120,
		EndAt:       130,
		ChunkType:   types.ChunkTypeText,
	}
	seg := SegmentForChunk(chunk)

	if seg.SourceStart != 120 || seg.SourceEnd != 130 {
		t.Errorf("source range = [%d,%d), want [120,130)", seg.SourceStart, seg.SourceEnd)
	}
	if seg.Text != chunk.Content {
		t.Errorf("text = %q, want %q", seg.Text, chunk.Content)
	}
	if seg.ChunkID != chunk.ID || seg.KnowledgeID != chunk.KnowledgeID {
		t.Errorf("identity = %q/%q, want %q/%q", seg.ChunkID, seg.KnowledgeID, chunk.ID, chunk.KnowledgeID)
	}
	if seg.ChunkType != string(types.ChunkTypeText) {
		t.Errorf("chunk_type = %q, want %q", seg.ChunkType, types.ChunkTypeText)
	}
	if seg.SourceEnd-seg.SourceStart != len([]rune(seg.Text)) {
		t.Errorf("exact-slice invariant violated: range %d != runeLen(text) %d",
			seg.SourceEnd-seg.SourceStart, len([]rune(seg.Text)))
	}
}

func TestSegmentForChunkUntrustedDegradesToSnapshot(t *testing.T) {
	chunk := &types.Chunk{
		ID:              "chunk-2",
		KnowledgeID:     "knowledge-1",
		Content:         "编辑后的内容",
		StartAt:         10,
		EndAt:           20,
		ContentRevision: 2,
		ChunkType:       types.ChunkTypeText,
	}
	seg := SegmentForChunk(chunk)

	if seg.SourceStart != 0 || seg.SourceEnd != 0 {
		t.Errorf("untrusted chunk must degrade to [0,0), got [%d,%d)", seg.SourceStart, seg.SourceEnd)
	}
	if seg.Text == "" {
		t.Error("snapshot segment text must be non-empty")
	}
	if seg.Text != chunk.Content {
		t.Errorf("text = %q, want %q", seg.Text, chunk.Content)
	}
	if seg.ChunkID != chunk.ID || seg.KnowledgeID != chunk.KnowledgeID {
		t.Errorf("identity = %q/%q, want %q/%q", seg.ChunkID, seg.KnowledgeID, chunk.ID, chunk.KnowledgeID)
	}
	if seg.ChunkType != string(types.ChunkTypeText) {
		t.Errorf("chunk_type = %q, want %q", seg.ChunkType, types.ChunkTypeText)
	}
}
