package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// populateFAQAnswers replaces the result content with the generated answer;
// the stale creation segment must be reset to a [0,0) snapshot marker.
func TestPopulateFAQAnswersResetsSegmentsToSnapshot(t *testing.T) {
	faqMeta := types.FAQChunkMetadata{
		StandardQuestion: "如何重置密码",
		Answers:          []string{"进入设置页", "点击重置"},
	}
	faqChunk := &types.Chunk{
		ID:        "faq-1",
		ChunkType: types.ChunkTypeFAQ,
	}
	if err := faqChunk.SetFAQMetadata(&faqMeta); err != nil {
		t.Fatal(err)
	}

	repo := &expandChunkRepo{chunks: map[string]*types.Chunk{"faq-1": faqChunk}}
	plugin := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	result := &types.SearchResult{
		ID:        "faq-1",
		ChunkType: string(types.ChunkTypeFAQ),
		Content:   "如何重置密码",
		ContentSegments: []types.ContentSegment{{
			Text: "如何重置密码", ChunkID: "faq-1", KnowledgeID: "k1",
			SourceStart: 0, SourceEnd: 6, ChunkType: string(types.ChunkTypeFAQ),
		}},
	}

	out := plugin.populateFAQAnswers(ctx, &types.ChatManage{}, []*types.SearchResult{result})
	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	r := out[0]
	if !strings.Contains(r.Content, "进入设置页") {
		t.Fatalf("content was not replaced with the answer: %q", r.Content)
	}
	if len(r.ContentSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(r.ContentSegments))
	}
	seg := r.ContentSegments[0]
	if seg.SourceStart != 0 || seg.SourceEnd != 0 {
		t.Errorf("FAQ segment must reset to [0,0), got [%d,%d)", seg.SourceStart, seg.SourceEnd)
	}
	if seg.Text != r.Content {
		t.Errorf("snapshot text = %q, want final content %q", seg.Text, r.Content)
	}
	if seg.Text == "" {
		t.Error("snapshot segment text must be non-empty")
	}
	if seg.ChunkID != "faq-1" {
		t.Errorf("chunk_id = %q, want faq-1", seg.ChunkID)
	}
}
