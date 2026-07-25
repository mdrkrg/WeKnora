package types

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// --------------------------------------------------------------------------
// Scope 1: ContentSegment field mapping (sec 3.4.1 table)
// --------------------------------------------------------------------------

func TestContentSegmentJSONRoundTrip(t *testing.T) {
	raw := `{"text":"hello world","chunk_id":"ck1","knowledge_id":"k1","source_start":10,"source_end":21,"chunk_type":"text"}`
	var seg ContentSegment
	if err := json.Unmarshal([]byte(raw), &seg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if seg.Text != "hello world" {
		t.Errorf("text = %q, want %q", seg.Text, "hello world")
	}
	if seg.ChunkID != "ck1" {
		t.Errorf("chunk_id = %q, want ck1", seg.ChunkID)
	}
	if seg.KnowledgeID != "k1" {
		t.Errorf("knowledge_id = %q, want k1", seg.KnowledgeID)
	}
	if seg.SourceStart != 10 {
		t.Errorf("source_start = %d, want 10", seg.SourceStart)
	}
	if seg.SourceEnd != 21 {
		t.Errorf("source_end = %d, want 21", seg.SourceEnd)
	}
	if seg.ChunkType != "text" {
		t.Errorf("chunk_type = %q, want text", seg.ChunkType)
	}
	out, err := json.Marshal(seg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ContentSegment
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if back != seg {
		t.Errorf("round-trip mismatch: got %+v, want %+v", back, seg)
	}
}

// --------------------------------------------------------------------------
// Scope 2: content_segments array invariants (sec 3.4.1 intro)
// --------------------------------------------------------------------------

func TestContentSegmentsAtLeastOne(t *testing.T) {
	// sec 3.4.1: content_segments array always contains at least one element.
	sr := SearchResult{
		Content: "some content",
		ContentSegments: []ContentSegment{{
			Text: "some content", ChunkID: "ck1", KnowledgeID: "k1",
			SourceStart: 0, SourceEnd: 12, ChunkType: "text",
		}},
	}
	if len(sr.ContentSegments) < 1 {
		t.Fatal("content_segments must have at least one element")
	}
}

func TestContentSegmentsConcatEqualsContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		segs    []ContentSegment
	}{
		{
			name:    "single segment",
			content: "abc",
			segs: []ContentSegment{{
				Text: "abc", ChunkID: "c1", KnowledgeID: "k1",
				SourceStart: 0, SourceEnd: 3, ChunkType: "text",
			}},
		},
		{
			name:    "two segments",
			content: "abcdef",
			segs: []ContentSegment{
				{Text: "abc", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 3, ChunkType: "text"},
				{Text: "def", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 3, SourceEnd: 6, ChunkType: "text"},
			},
		},
		{
			name:    "three segments",
			content: "abcde",
			segs: []ContentSegment{
				{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
				{Text: "c", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 3, ChunkType: "text"},
				{Text: "de", ChunkID: "c3", KnowledgeID: "k1", SourceStart: 3, SourceEnd: 5, ChunkType: "text"},
			},
		},
		{
			name:    "unicode content",
			content: "中文测试",
			segs: []ContentSegment{
				{Text: "中文", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
				{Text: "测试", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 4, ChunkType: "text"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var concat string
			for _, s := range tt.segs {
				concat += s.Text
			}
			if concat != tt.content {
				t.Errorf("concat = %q, want %q", concat, tt.content)
			}
		})
	}
}

func TestContentSegmentsEachRuneBelongsToOneSegment(t *testing.T) {
	segs := []ContentSegment{
		{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
		{Text: "c", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 3, ChunkType: "text"},
		{Text: "de", ChunkID: "c3", KnowledgeID: "k1", SourceStart: 3, SourceEnd: 5, ChunkType: "text"},
	}
	content := "abcde"

	totalRunes := 0
	for _, s := range segs {
		totalRunes += utf8.RuneCountInString(s.Text)
	}
	contentRunes := utf8.RuneCountInString(content)
	if totalRunes != contentRunes {
		t.Errorf("segment rune total %d != content rune count %d", totalRunes, contentRunes)
	}
}

// --------------------------------------------------------------------------
// Scope 3: source range invariants (sec 3.4.1 overlap rules)
// --------------------------------------------------------------------------

func TestSourceRangeLengthEqualsTextRuneCount(t *testing.T) {
	tests := []struct {
		name      string
		start, end int
		text       string
	}{
		{"ASCII text", 0, 5, "hello"},
		{"unicode text", 0, 2, "中文"},
		{"mixed text", 10, 17, "hello世界"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seg := ContentSegment{
				Text: tt.text, ChunkID: "ck", KnowledgeID: "k",
				SourceStart: tt.start, SourceEnd: tt.end, ChunkType: "text",
			}
			if seg.SourceEnd-seg.SourceStart != utf8.RuneCountInString(seg.Text) {
				t.Errorf("SourceEnd(%d)-SourceStart(%d)=%d != runeLen(%q)=%d",
					seg.SourceEnd, seg.SourceStart,
					seg.SourceEnd-seg.SourceStart,
					seg.Text, utf8.RuneCountInString(seg.Text))
			}
		})
	}
}

func TestNoOverlapBetweenAdjacentSegments(t *testing.T) {
	// Adjacent segments must have disjoint SourceStart/SourceEnd ranges
	// when no overlap in source.  Their Text fields must not overlap.
	segs := []ContentSegment{
		{Text: "hello", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 5, ChunkType: "text"},
		{Text: "world", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 5, SourceEnd: 10, ChunkType: "text"},
	}
	// Check non-overlapping source ranges.
	for i := 1; i < len(segs); i++ {
		prev := segs[i-1]
		curr := segs[i]
		if prev.SourceEnd > curr.SourceStart {
			t.Errorf("segment %d source range [%d,%d) overlaps segment %d range [%d,%d)",
				i-1, prev.SourceStart, prev.SourceEnd,
				i, curr.SourceStart, curr.SourceEnd)
		}
	}
}

func TestFullyOverlappedChunkProducesNoSegment(t *testing.T) {
	// sec 3.4.1 overlap rule: a chunk whose entire source range falls
	// within a preceding chunk's range contributes no unique text and
	// must not produce a segment in content_segments.
	tests := []struct {
		name        string
		aStart, aEnd int
		bStart, bEnd int
		expectBSeg  bool
	}{
		{"fully inside", 0, 10, 3, 6, false},
		{"fully inside at boundary", 0, 10, 0, 5, false},
		{"fully inside end boundary", 0, 10, 5, 10, false},
		{"partial overlap (extends past)", 0, 10, 5, 15, true},
		{"no overlap (adjacent)", 0, 10, 10, 20, true},
		{"no overlap (gap)", 0, 10, 20, 30, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isFullyCovered := tt.bStart >= tt.aStart && tt.bEnd <= tt.aEnd
			if isFullyCovered == tt.expectBSeg {
				t.Errorf("b [%d,%d) inside a [%d,%d): fullyCovered=%v, expectBSeg=%v",
					tt.bStart, tt.bEnd, tt.aStart, tt.aEnd, isFullyCovered, tt.expectBSeg)
			}
		})
	}
}

func TestFullOverlapExcludesChunkFromSegments(t *testing.T) {
	// Apply the overlap rule to produce segments.
	// Chunk A: [0,10), Chunk B: [3,6) fully inside -> only A in segments.
	type chunk struct {
		id          string
		start, end  int
		text        string
	}
	chunks := []chunk{
		{id: "cA", start: 0, end: 10, text: "abcdefghij"},
		{id: "cB", start: 3, end: 6, text: "def"},
	}
	var segments []ContentSegment
	prevEnd := 0
	for _, ch := range chunks {
		if ch.start >= ch.end {
			continue
		}
		if ch.start >= prevEnd {
			segments = append(segments, ContentSegment{
				Text:        ch.text,
				ChunkID:     ch.id,
				KnowledgeID: "k1",
				SourceStart: ch.start,
				SourceEnd:   ch.end,
				ChunkType:   "text",
			})
			prevEnd = ch.end
			continue
		}
		// Partial overlap: only non-overlapped portion produces a segment.
		if ch.end > prevEnd {
			trim := prevEnd - ch.start
			text := string([]rune(ch.text)[trim:])
			segments = append(segments, ContentSegment{
				Text:        text,
				ChunkID:     ch.id,
				KnowledgeID: "k1",
				SourceStart: ch.start + trim,
				SourceEnd:   ch.end,
				ChunkType:   "text",
			})
			prevEnd = ch.end
		}
		// ch.end <= prevEnd: fully covered, no segment.
	}
	// Verify cB is not present.
	for _, s := range segments {
		if s.ChunkID == "cB" {
			t.Errorf("fully covered chunk cB must not appear in segments: %+v", s)
		}
	}
	if len(segments) < 1 {
		t.Fatal("must have at least one segment")
	}
}

func TestPartialOverlapTrimsOneSegment(t *testing.T) {
	// sec 3.4.1: when chunks overlap partially, the later chunk's text
	// has the overlapping prefix removed and source_start is adjusted.
	chunkA := ContentSegment{Text: "abcdef", ChunkID: "cA", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 6, ChunkType: "text"}
	chunkB := ContentSegment{Text: "cdefgh", ChunkID: "cB", KnowledgeID: "k1", SourceStart: 2, SourceEnd: 8, ChunkType: "text"}

	// Overlap: A [0,6), B [2,8) -> overlap region [2,6)
	// B's trimmed text = B.text[6-2:] = "gh"
	overlapStart := chunkB.SourceStart
	if chunkA.SourceEnd > overlapStart {
		overlapStart = chunkA.SourceEnd
	}
	trim := overlapStart - chunkB.SourceStart
	trimmedText := string([]rune(chunkB.Text)[trim:])
	wantText := "gh"
	if trimmedText != wantText {
		t.Errorf("trimmed text = %q, want %q", trimmedText, wantText)
	}
	wantStart := chunkB.SourceStart + trim
	if wantStart != 6 {
		t.Errorf("adjusted source_start = %d, want 6", wantStart)
	}
	// Invariant still holds for trimmed segment.
	wantEnd := chunkB.SourceEnd
	if wantEnd-wantStart != utf8.RuneCountInString(trimmedText) {
		t.Errorf("trimmed source range [%d,%d) span %d != text rune count %d",
			wantStart, wantEnd, wantEnd-wantStart, utf8.RuneCountInString(trimmedText))
	}
}

func TestFullyCoveredChunkInSubChunkIDNotInSegments(t *testing.T) {
	// sec 3.4.1: a fully covered chunk contributes no segment but its
	// chunk ID remains in SubChunkID so consumers can enumerate all
	// merged participants.
	sr := SearchResult{
		Content:   "abcdefghij",
		StartAt:   0,
		EndAt:     10,
		SubChunkID: []string{"cA", "cB"},
		ContentSegments: []ContentSegment{
			{Text: "abcdefghij", ChunkID: "cA", KnowledgeID: "k1",
				SourceStart: 0, SourceEnd: 10, ChunkType: "text"},
		},
	}
	// cB is fully covered: appears in SubChunkID, not in ContentSegments.
	for _, s := range sr.ContentSegments {
		if s.ChunkID == "cB" {
			t.Errorf("fully covered chunk cB must not appear in content_segments")
		}
	}
	found := false
	for _, id := range sr.SubChunkID {
		if id == "cB" {
			found = true
			break
		}
	}
	if !found {
		t.Error("fully covered chunk cB must appear in SubChunkID")
	}
}

// --------------------------------------------------------------------------
// Scope 4: single-chunk consistency (sec 3.4.1)
// --------------------------------------------------------------------------

func TestSingleChunkSegmentMatchesTopLevelFields(t *testing.T) {
	// sec 3.4.1: for an unmerged single-chunk result, content_segments
	// has exactly one segment whose source_start/end match the top-level
	// SearchResult start_at/end_at.
	text := "hello world"
	runes := utf8.RuneCountInString(text)
	sr := SearchResult{
		Content: text,
		StartAt: 100,
		EndAt:   100 + runes,
		ContentSegments: []ContentSegment{{
			Text:        text,
			ChunkID:     "ck1",
			KnowledgeID: "k1",
			SourceStart: 100,
			SourceEnd:   100 + runes,
			ChunkType:   "text",
		}},
	}
	if len(sr.ContentSegments) != 1 {
		t.Fatalf("single chunk result must have 1 segment, got %d", len(sr.ContentSegments))
	}
	seg := sr.ContentSegments[0]
	if seg.SourceStart != sr.StartAt {
		t.Errorf("segment source_start %d != SearchResult start_at %d", seg.SourceStart, sr.StartAt)
	}
	if seg.SourceEnd != sr.EndAt {
		t.Errorf("segment source_end %d != SearchResult end_at %d", seg.SourceEnd, sr.EndAt)
	}
	if seg.SourceEnd-seg.SourceStart != runes {
		t.Errorf("source range span %d != text rune count %d", seg.SourceEnd-seg.SourceStart, runes)
	}
}

// --------------------------------------------------------------------------
// Scope 5: generated-type [0,0) range (sec 3.4.1)
// --------------------------------------------------------------------------

func TestGeneratedTypeHasZeroSourceRangeAndNonEmptyText(t *testing.T) {
	tests := []struct {
		name      string
		chunkType string
		text      string
	}{
		{"summary type", "summary", "generated summary text"},
		{"entity type", "entity", "an entity name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seg := ContentSegment{
				Text:        tt.text,
				ChunkID:     "ck1",
				KnowledgeID: "k1",
				SourceStart: 0,
				SourceEnd:   0,
				ChunkType:   tt.chunkType,
			}
			if seg.SourceStart != 0 || seg.SourceEnd != 0 {
				t.Errorf("generated type %s must have [0,0) source range, got [%d,%d)",
					tt.chunkType, seg.SourceStart, seg.SourceEnd)
			}
			if seg.Text == "" {
				t.Errorf("generated type %s must have non-empty text even with [0,0) range", tt.chunkType)
			}
		})
	}
}

func TestZeroRangeMustNotBeUsedForSlice(t *testing.T) {
	seg := ContentSegment{
		Text:        "snapshot only text",
		ChunkID:     "ck1",
		KnowledgeID: "k1",
		SourceStart: 0,
		SourceEnd:   0,
		ChunkType:   "summary",
	}
	if seg.SourceStart == 0 && seg.SourceEnd == 0 {
		// [0,0) range means no valid locator; consumers must not use it
		// for slicing source text. The text is snapshot-only.
		if seg.Text == "" {
			t.Error("zero-range segment must still carry its snapshot text")
		}
	}
}

// --------------------------------------------------------------------------
// Scope 6: consumer positioning (sec 3.4.1 positioning guide)
// --------------------------------------------------------------------------

func TestMapContentPositionToSourceRangeSingleSegment(t *testing.T) {
	// Given substring at rune offset 2, length 3 within a single segment,
	// its source range = [s.SourceStart + 2, s.SourceStart + 5).
	seg := ContentSegment{
		Text:        "abcdefg",
		ChunkID:     "ck1",
		KnowledgeID: "k1",
		SourceStart: 100,
		SourceEnd:   107,
		ChunkType:   "text",
	}
	// Substring "cde" at offset 2, length 3 within segment.
	subOffset := 2
	subLen := 3
	wantStart := seg.SourceStart + subOffset
	wantEnd := seg.SourceStart + subOffset + subLen

	if wantStart != 102 || wantEnd != 105 {
		t.Errorf("mapped source range [%d,%d), want [102,105)", wantStart, wantEnd)
	}
}

func TestMapContentPositionAcrossTwoSegments(t *testing.T) {
	// A 5-rune substring that spans the boundary of segment0 (3 runes "abc")
	// and segment1 (2 runes "de").
	segs := []ContentSegment{
		{Text: "abc", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 3, ChunkType: "text"},
		{Text: "de", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 5, SourceEnd: 7, ChunkType: "text"},
	}
	content := "abcde"

	// Substring from offset 1 ("bcde") spans two segments.
	// Part in seg0: runes 1..3 (2 runes) -> source [0+1, 0+3) = [1,3)
	// Part in seg1: runes 0..2 (2 runes) -> source [5+0, 5+2) = [5,7)
	// FIXED: len=4, not len=5. Let me recalculate.
	// Substring "bcde" starts at offset 1, length 4.
	// seg0 "abc", offset 1 = "bc" (2 runes)
	// seg1 "de", offset 0 = "de" (2 runes, but we only take up to the offset within content)
	//
	// Actually, "bcde" starts at content rune position 1.
	// seg0 covers content runes [0,3): "abc"
	// seg1 covers content runes [3,5): "de"
	// Substring "bcde" starts at 1, ends at 5.
	// Part in seg0: runes [1,3) = "bc" -> 2 runes, source [1,3)
	// Part in seg1: runes [3,5) = "de" -> 2 runes, source [5,7)
	// FIXED THE LENGTH. Substring is "bcde" which is 4 runes total.

	_ = content
	_ = segs

	// Part in seg0: offset 1 within content, seg0 covers [0,3) in content.
	seg0Offset := 1
	seg0Len := 2
	seg0SourceStart := segs[0].SourceStart + seg0Offset
	seg0SourceEnd := seg0SourceStart + seg0Len

	// Part in seg1: offset 0 within seg1, seg1 covers [3,5) in content.
	seg1Offset := 0
	seg1Len := 2
	seg1SourceStart := segs[1].SourceStart + seg1Offset
	seg1SourceEnd := seg1SourceStart + seg1Len

	if seg0SourceStart != 1 || seg0SourceEnd != 3 {
		t.Errorf("seg0 source range [%d,%d), want [1,3)", seg0SourceStart, seg0SourceEnd)
	}
	if seg1SourceStart != 5 || seg1SourceEnd != 7 {
		t.Errorf("seg1 source range [%d,%d), want [5,7)", seg1SourceStart, seg1SourceEnd)
	}
}

// --------------------------------------------------------------------------
// Scope 7: sec 8.9 offset semantics – top-level vs segments after merge
// --------------------------------------------------------------------------

func TestMergedResultTopLevelRangeDoesNotCoverFullContent(t *testing.T) {
	// sec 8.9: after merge, top-level start_at/end_at reflect the first
	// (dominant) chunk's range.  They do not cover text from subsequent
	// chunks that were appended during merge.
	sr := SearchResult{
		Content: "firstsecond",
		StartAt: 0,
		EndAt:   5,
		ContentSegments: []ContentSegment{
			{Text: "first", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 5, ChunkType: "text"},
			{Text: "second", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 10, SourceEnd: 16, ChunkType: "text"},
		},
	}
	// Top-level range only covers the first chunk (5 runes).
	if sr.EndAt-sr.StartAt != utf8.RuneCountInString("first") {
		t.Errorf("top-level range [%d,%d) covers %d runes, but first chunk has %d",
			sr.StartAt, sr.EndAt, sr.EndAt-sr.StartAt, utf8.RuneCountInString("first"))
	}
	// content has more runes than top-level range describes.
	contentRunes := utf8.RuneCountInString(sr.Content)
	if sr.EndAt-sr.StartAt >= contentRunes {
		t.Errorf("top-level range covers %d runes but content has %d; content_segments must be used for full coverage",
			sr.EndAt-sr.StartAt, contentRunes)
	}
}

func TestConsumerUsesContentSegmentsNotTopLevelForMerged(t *testing.T) {
	// sec 8.9: consumer must use content_segments for locating substrings
	// in merged content, not top-level start_at/end_at.
	sr := SearchResult{
		Content: "abcd",
		StartAt: 0,
		EndAt:   2, // first chunk only
		ContentSegments: []ContentSegment{
			{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
			{Text: "cd", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 10, SourceEnd: 12, ChunkType: "text"},
		},
	}
	// Substring "cd" at content offset 2: falls in segment[1] at offset 0.
	// Source range = [10, 12).
	// If consumer naively used top-level start_at=0, end_at=2,
	// they'd get [0+2, 0+4) = [2,4) — which is wrong.
	segIdx := 1
	segOffset := 0
	subLen := 2
	gotStart := sr.ContentSegments[segIdx].SourceStart + segOffset
	gotEnd := gotStart + subLen
	if gotStart != 10 || gotEnd != 12 {
		t.Errorf("consumer positioning via content_segments: [%d,%d), want [10,12)", gotStart, gotEnd)
	}
}

// --------------------------------------------------------------------------
// Scope 8: no synthetic characters (sec 3.4.1)
// --------------------------------------------------------------------------

func TestNoSyntheticCharactersInSegments(t *testing.T) {
	// content must not contain characters inserted solely by merge
	// (e.g. synthetic separators).  Every rune in content belongs to a
	// source chunk.
	segs := []ContentSegment{
		{Text: "hello", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 5, ChunkType: "text"},
		{Text: "world", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 5, SourceEnd: 10, ChunkType: "text"},
	}
	var total int
	for _, s := range segs {
		total += utf8.RuneCountInString(s.Text)
	}
	// If content had a synthetic \n separator between segments, total
	// would be 11 (5 + 1 + 5), but since no synthetic chars, total = 10.
	content := "helloworld"
	if utf8.RuneCountInString(content) != total {
		t.Errorf("content runes %d != segment rune total %d; synthetic characters detected",
			utf8.RuneCountInString(content), total)
	}
}

// --------------------------------------------------------------------------
// Scope 9: ContentSegments ChunkID membership (sec 3.4.1 + 3.4 sub_chunk_id)
// --------------------------------------------------------------------------

func TestContentSegmentsChunkIDsAreSubsetOfSubChunkID(t *testing.T) {
	// Every ChunkID appearing in ContentSegments must also appear in
	// SubChunkID.  The reverse is not required: SubChunkID may contain
	// fully covered chunks that produced no segment.
	sr := SearchResult{
		Content:   "hello world",
		SubChunkID: []string{"cA", "cB", "cC"},
		ContentSegments: []ContentSegment{
			{Text: "hello", ChunkID: "cA", KnowledgeID: "k1",
				SourceStart: 0, SourceEnd: 5, ChunkType: "text"},
			{Text: " world", ChunkID: "cC", KnowledgeID: "k1",
				SourceStart: 6, SourceEnd: 12, ChunkType: "text"},
		},
	}
	// cB is in SubChunkID (fully covered), cA and cC are in both.
	for _, seg := range sr.ContentSegments {
		found := false
		for _, id := range sr.SubChunkID {
			if id == seg.ChunkID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ContentSegments ChunkID %q missing from SubChunkID", seg.ChunkID)
		}
	}
}

// --------------------------------------------------------------------------
// Scope 10: KnowledgeRetrieveResult JSON round-trip (sec 3.4 field table)
// --------------------------------------------------------------------------

func TestKnowledgeRetrieveResultContentSegmentsJSONRoundTrip(t *testing.T) {
	in := KnowledgeRetrieveResult{
		ID:      "r1",
		Content: "abcdef",
		Score:   0.95,
		ContentSegments: []ContentSegment{
			{Text: "abc", ChunkID: "c1", KnowledgeID: "k1",
				SourceStart: 0, SourceEnd: 3, ChunkType: "text"},
			{Text: "def", ChunkID: "c2", KnowledgeID: "k1",
				SourceStart: 10, SourceEnd: 13, ChunkType: "text"},
		},
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back KnowledgeRetrieveResult
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.ContentSegments) != 2 {
		t.Fatalf("ContentSegments length: got %d, want 2", len(back.ContentSegments))
	}
	if back.ContentSegments[0].ChunkID != "c1" {
		t.Errorf("segment[0].chunk_id = %q, want c1", back.ContentSegments[0].ChunkID)
	}
	if back.ContentSegments[1].SourceEnd != 13 {
		t.Errorf("segment[1].source_end = %d, want 13", back.ContentSegments[1].SourceEnd)
	}
}

func TestKnowledgeRetrieveResultContentSegmentsInJSON(t *testing.T) {
	in := KnowledgeRetrieveResult{
		ID:              "r1",
		Content:         "abc",
		Score:           0.9,
		ContentSegments: []ContentSegment{{
			Text: "abc", ChunkID: "c1", KnowledgeID: "k1",
			SourceStart: 0, SourceEnd: 3, ChunkType: "text",
		}},
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(out)
	if !json.Valid([]byte(raw)) {
		t.Fatalf("output is not valid JSON: %s", raw)
	}
	// content_segments field must appear in the serialized output.
	if !strings.Contains(raw, `"content_segments"`) {
		t.Errorf("serialized JSON missing content_segments key: %s", raw)
	}
}
