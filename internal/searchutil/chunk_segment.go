package searchutil

import (
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// ChunkRangeTrusted reports whether a chunk's stored content is consistent
// with its parser coordinates, so that artifact[StartAt:EndAt] == Content
// holds. Edited chunks (ContentRevision > 0) and chunks with a missing or
// length-inconsistent range cannot be mapped back to an exact artifact
// slice and must not produce locatable segments.
func ChunkRangeTrusted(chunk *types.Chunk) bool {
	if chunk == nil || chunk.ContentRevision != 0 || chunk.EndAt <= chunk.StartAt {
		return false
	}
	return utf8.RuneCountInString(chunk.Content) == chunk.EndAt-chunk.StartAt
}

// SegmentForChunk builds the initial ContentSegment for a chunk. Range-trusted
// chunks yield an exact-slice segment mapping Content back to the source
// artifact; untrusted chunks explicitly degrade to a [0,0) snapshot segment
// with non-empty text, per docs/knowledge-retrieve-spec.md sec 3.4.1.
func SegmentForChunk(chunk *types.Chunk) types.ContentSegment {
	seg := types.ContentSegment{
		Text:        chunk.Content,
		ChunkID:     chunk.ID,
		KnowledgeID: chunk.KnowledgeID,
		ChunkType:   string(chunk.ChunkType),
	}
	if ChunkRangeTrusted(chunk) {
		seg.SourceStart = chunk.StartAt
		seg.SourceEnd = chunk.EndAt
	}
	return seg
}
