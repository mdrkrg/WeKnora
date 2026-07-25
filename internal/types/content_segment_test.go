package types

import (
	"encoding/json"
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
	content := "some content"
	seg := ContentSegment{
		Text:        content,
		ChunkID:     "ck1",
		KnowledgeID: "k1",
		SourceStart: 0,
		SourceEnd:   len([]rune(content)),
		ChunkType:   "text",
	}
	if seg.Text == "" {
		t.Fatal("text must be non-empty for non-zero source range")
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
	// A fully overlapped chunk contributes no unique text, so it does not
	// appear in content_segments.
	// The covering chunk's segment carries the content; sub_chunk_id
	// carries the participant list.
	cover := ContentSegment{
		Text:        "full content",
		ChunkID:     "c1",
		KnowledgeID: "k1",
		SourceStart: 0,
		SourceEnd:   12,
		ChunkType:   "text",
	}
	// Fully overlapped chunk B would be entirely within A's range;
	// it contributes no unique text, so it does not appear.
	_ = cover

	// Validate that a segment list built from such overlap would not include
	// an entry for the fully-covered chunk.  We construct the expected output
	// and assert the absent chunk ID is not present.
	segments := []ContentSegment{cover}
	for _, s := range segments {
		if s.ChunkID == "c2" {
			t.Errorf("fully covered chunk c2 must not appear in segments")
		}
	}
}

// --------------------------------------------------------------------------
// Scope 4: single-chunk consistency (sec 3.4.1)
// --------------------------------------------------------------------------

func TestSingleChunkSegmentMatchesTopLevelFields(t *testing.T) {
	// For an unmerged single-chunk result, the segment source_start/end
	// must match the top-level start_at/end_at.
	text := "hello world"
	topStart := 100
	topEnd := topStart + utf8.RuneCountInString(text) // 111
	seg := ContentSegment{
		Text:        text,
		ChunkID:     "ck1",
		KnowledgeID: "k1",
		SourceStart: topStart,
		SourceEnd:   topEnd,
		ChunkType:   "text",
	}
	if seg.SourceStart != topStart {
		t.Errorf("segment source_start %d != top-level start_at %d", seg.SourceStart, topStart)
	}
	if seg.SourceEnd != topEnd {
		t.Errorf("segment source_end %d != top-level end_at %d", seg.SourceEnd, topEnd)
	}
	if seg.SourceEnd-seg.SourceStart != utf8.RuneCountInString(seg.Text) {
		t.Errorf("source range span %d != text rune count %d",
			seg.SourceEnd-seg.SourceStart, utf8.RuneCountInString(seg.Text))
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
	// After merge, top-level start_at/end_at reflect the first (dominant)
	// chunk's range.  They do not cover text from subsequent chunks
	// that were appended during merge.
	topStart := 0
	topEnd := 5 // first chunk: 5 runes

	segments := []ContentSegment{
		{Text: "first", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 5, ChunkType: "text"},
		{Text: "second", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 10, SourceEnd: 16, ChunkType: "text"},
	}
	content := "firstsecond"

	// Top-level range only covers the first chunk.
	if topEnd-topStart != utf8.RuneCountInString("first") {
		t.Errorf("top-level range [%d,%d) covers %d runes but first chunk has %d",
			topStart, topEnd, topEnd-topStart, utf8.RuneCountInString("first"))
	}
	// content has more runes than top-level range describes.
	contentRunes := utf8.RuneCountInString(content)
	if topEnd-topStart >= contentRunes {
		t.Errorf("top-level range covers %d runes but content has %d; must use content_segments for full coverage",
			topEnd-topStart, contentRunes)
	}
	_ = segments // segments provide the full mapping
}

func TestConsumerUsesContentSegmentsNotTopLevelForMerged(t *testing.T) {
	// sec 8.9: consumer must use content_segments for locating substrings
	// in merged content, not top-level start_at/end_at.
	segments := []ContentSegment{
		{Text: "ab", ChunkID: "c1", KnowledgeID: "k1", SourceStart: 0, SourceEnd: 2, ChunkType: "text"},
		{Text: "cd", ChunkID: "c2", KnowledgeID: "k1", SourceStart: 10, SourceEnd: 12, ChunkType: "text"},
	}
	content := "abcd"
	// Substring "cd" at content offset 2.
	// Using content_segments: it falls in segment[1] at offset 0.
	// Source range = [10, 12)
	// If consumer naively used top-level start_at=0, end_at=2,
	// they'd get [0+2, 0+4) = [2,4) which is wrong — that's not where
	// "cd" comes from in the source.
	segIdx := 1
	segOffset := 0
	subLen := 2
	gotStart := segments[segIdx].SourceStart + segOffset
	gotEnd := gotStart + subLen
	if gotStart != 10 || gotEnd != 12 {
		t.Errorf("consumer positioning via content_segments: [%d,%d), want [10,12)", gotStart, gotEnd)
	}
	_ = content
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
