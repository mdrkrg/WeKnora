package chatpipeline

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// mergeOverlappingChunks merges chunks with overlapping or adjacent StartAt/EndAt
// ranges within a single knowledge source group. Chunks MUST be pre-sorted by
// StartAt ascending, EndAt ascending. The highest score among merged chunks is kept.
func (p *PluginMerge) mergeOverlappingChunks(
	ctx context.Context,
	knowledgeID string,
	chunks []*types.SearchResult,
) []*types.SearchResult {
	if len(chunks) == 0 {
		return nil
	}

	merged := []*types.SearchResult{chunks[0]}
	for i := 1; i < len(chunks); i++ {
		lastChunk := merged[len(merged)-1]

		// Non-overlapping: add as a new entry
		if chunks[i].StartAt > lastChunk.EndAt {
			merged = append(merged, chunks[i])
			continue
		}

		// Partial overlap: append the non-overlapping suffix.
		//
		// 重叠去重统一交给 searchutil.AppendWithOverlap（按文本匹配），它能
		// 兼容父子分块器补写的零宽表头，以及 HTML 实体导致的 content 长度与
		// EndAt-StartAt 不一致——这两种情况都会让按位置裁剪错位、丢字或重复。
		// StartAt/EndAt 仅用于估算搜索窗口大小。
		if chunks[i].EndAt > lastChunk.EndAt {
			beforeLen := runeLen(lastChunk.Content)
			lastChunk.Content = searchutil.AppendWithOverlap(
				lastChunk.Content, chunks[i].Content, lastChunk.EndAt-chunks[i].StartAt,
			)
			appendedLen := runeLen(lastChunk.Content) - beforeLen
			lastChunk.EndAt = chunks[i].EndAt
			lastChunk.SubChunkID = append(lastChunk.SubChunkID, chunks[i].ID)

			// Adjust the incoming chunk's segments: trim the overlap prefix.
			overlapLen := runeLen(chunks[i].Content) - appendedLen
			if overlapLen > 0 && overlapLen < runeLen(chunks[i].Content) {
				p.addTrimmedSegments(lastChunk, chunks[i], overlapLen)
			} else if overlapLen == 0 {
				p.copySegments(lastChunk, chunks[i])
			}
			// overlapLen == len(chunks[i].Content): fully covered, no segment.

			if err := mergeImageInfo(ctx, lastChunk, chunks[i]); err != nil {
				pipelineWarn(ctx, "Merge", "image_merge", map[string]interface{}{
					"knowledge_id": knowledgeID,
					"error":        err.Error(),
				})
			}
		} else {
			// Fully contained: track the subsumed chunk and merge its ImageInfo
			if !containsID(lastChunk.SubChunkID, chunks[i].ID) {
				lastChunk.SubChunkID = append(lastChunk.SubChunkID, chunks[i].ID)
			}
			if err := mergeImageInfo(ctx, lastChunk, chunks[i]); err != nil {
				pipelineWarn(ctx, "Merge", "image_merge_contained", map[string]interface{}{
					"knowledge_id": knowledgeID,
					"error":        err.Error(),
				})
			}
		}

		// Keep the higher score
		if chunks[i].Score > lastChunk.Score {
			lastChunk.Score = chunks[i].Score
		}
	}

	// Sort merged chunks by score (highest first)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	return merged
}

// mergeImageInfo merges ImageInfo from source into target, deduplicating by URL.
func mergeImageInfo(ctx context.Context, target *types.SearchResult, source *types.SearchResult) error {
	if source.ImageInfo == "" {
		return nil
	}

	var sourceImageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(source.ImageInfo), &sourceImageInfos); err != nil {
		pipelineWarn(ctx, "Merge", "image_unmarshal_source", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	if len(sourceImageInfos) == 0 {
		return nil
	}

	var targetImageInfos []types.ImageInfo
	if target.ImageInfo != "" {
		if err := json.Unmarshal([]byte(target.ImageInfo), &targetImageInfos); err != nil {
			pipelineWarn(ctx, "Merge", "image_unmarshal_target", map[string]interface{}{
				"error": err.Error(),
			})
			target.ImageInfo = source.ImageInfo
			return nil
		}
	}

	targetImageInfos = append(targetImageInfos, sourceImageInfos...)

	uniqueMap := make(map[string]bool)
	uniqueImageInfos := make([]types.ImageInfo, 0, len(targetImageInfos))

	for _, imgInfo := range targetImageInfos {
		if imgInfo.URL != "" && !uniqueMap[imgInfo.URL] {
			uniqueMap[imgInfo.URL] = true
			uniqueImageInfos = append(uniqueImageInfos, imgInfo)
		}
	}

	mergedImageInfoJSON, err := json.Marshal(uniqueImageInfos)
	if err != nil {
		pipelineWarn(ctx, "Merge", "image_marshal", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	target.ImageInfo = string(mergedImageInfoJSON)
	pipelineInfo(ctx, "Merge", "image_merged", map[string]interface{}{
		"image_refs": len(uniqueImageInfos),
	})
	return nil
}

// addTrimmedSegments copies segments from src to dst, trimming overlapLen
// runes from the front of the concatenated text across all segments.
// Trimming stops once overlapLen runes have been consumed; fully consumed
// segments are skipped.  source_start on the first partially consumed
// segment is adjusted accordingly.
func (p *PluginMerge) addTrimmedSegments(dst *types.SearchResult, src *types.SearchResult, overlapLen int) {
	remaining := overlapLen
	for _, seg := range src.ContentSegments {
		runes := []rune(seg.Text)
		if remaining > 0 {
			if remaining >= len(runes) {
				remaining -= len(runes)
				continue
			}
			seg.Text = string(runes[remaining:])
			seg.SourceStart = seg.SourceStart + remaining
			remaining = 0
		}
		dst.ContentSegments = append(dst.ContentSegments, seg)
	}
}

// copySegments appends all segments from src to dst without modification.
func (p *PluginMerge) copySegments(dst *types.SearchResult, src *types.SearchResult) {
	dst.ContentSegments = append(dst.ContentSegments, src.ContentSegments...)
}
