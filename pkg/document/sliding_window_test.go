package document

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWithSlidingWindow(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		chunkSize  int
		overlap    int
		wantBodies []string
	}{
		{
			name:       "basic_no_overlap",
			content:    "aaa bbb ccc ddd",
			chunkSize:  8,
			overlap:    0,
			wantBodies: []string{"aaa bbb ", "ccc ddd"},
		},
		{
			name:      "basic_with_overlap",
			content:   "The quick brown fox jumps over the lazy dog",
			chunkSize: 20,
			overlap:   8,
			wantBodies: []string{
				"The quick brown fox ",
				"brown fox jumps over",
				"jumps over the lazy ",
				"the lazy dog",
			},
		},
		{
			name:       "content_smaller_than_chunk",
			content:    "tiny",
			chunkSize:  100,
			overlap:    10,
			wantBodies: []string{"tiny"},
		},
		{
			name:       "exact_chunk_size",
			content:    "abcd efgh",
			chunkSize:  9,
			overlap:    0,
			wantBodies: []string{"abcd efgh"},
		},
		{
			name:       "empty_content",
			content:    "",
			chunkSize:  10,
			overlap:    3,
			wantBodies: nil,
		},
		{
			name:       "zero_chunk_size",
			content:    "hello",
			chunkSize:  0,
			overlap:    0,
			wantBodies: nil,
		},
		{
			name:       "overlap_larger_than_chunk",
			content:    "aaa bbb ccc ddd eee",
			chunkSize:  8,
			overlap:    20,
			wantBodies: []string{"aaa bbb ", "ccc ddd ", "eee"},
		},
		{
			name:       "single_long_word_force_cut",
			content:    "abcdefghijklmnopqrstuvwxyz next",
			chunkSize:  10,
			overlap:    0,
			wantBodies: []string{"abcdefghij", "klmnopqrst", "uvwxyz ", "next"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := &Document{
				ID:       "sw-test",
				Content:  []byte(tc.content),
				Metadata: map[string]any{"format": "test"},
			}

			var bodies []string
			for chunk := range WithSlidingWindow(doc, tc.chunkSize, tc.overlap) {
				bodies = append(bodies, string(chunk.Content))
			}

			if len(bodies) != len(tc.wantBodies) {
				t.Fatalf("got %d chunks %v, want %d %v", len(bodies), bodies, len(tc.wantBodies), tc.wantBodies)
			}
			for i, got := range bodies {
				if got != tc.wantBodies[i] {
					t.Errorf("chunk[%d] = %q, want %q", i, got, tc.wantBodies[i])
				}
			}
		})
	}
}

func TestWithSlidingWindow_UTF8Safety(t *testing.T) {
	content := "café mundo bom dia feliz"
	doc := &Document{
		ID:       "utf8",
		Content:  []byte(content),
		Metadata: map[string]any{},
	}

	for chunk := range WithSlidingWindow(doc, 10, 3) {
		if !utf8.Valid(chunk.Content) {
			t.Fatalf("chunk %q contains invalid UTF-8", chunk.ID)
		}
	}
}

func TestWithSlidingWindow_UTF8MultiByte(t *testing.T) {
	content := "🔥🔥🔥 end"
	doc := &Document{
		ID:       "emoji",
		Content:  []byte(content),
		Metadata: map[string]any{},
	}

	for chunk := range WithSlidingWindow(doc, 5, 0) {
		if !utf8.Valid(chunk.Content) {
			t.Fatalf("chunk %q has invalid UTF-8: %x", chunk.ID, chunk.Content)
		}
	}
}

func TestWithSlidingWindow_IDs(t *testing.T) {
	doc := &Document{
		ID:       "doc-id",
		Content:  []byte("aaa bbb ccc ddd"),
		Metadata: map[string]any{},
	}

	var ids []string
	for chunk := range WithSlidingWindow(doc, 8, 0) {
		ids = append(ids, chunk.ID)
	}

	want := []string{"doc-id#w0", "doc-id#w1"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range ids {
		if ids[i] != want[i] {
			t.Errorf("id[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestWithSlidingWindow_MetadataSharing(t *testing.T) {
	// Chunks share doc.Metadata by reference. Mutations to chunk.Metadata
	// propagate to the source document and to all sibling chunks.
	// Callers that need per-chunk isolation must copy before mutating.
	doc := &Document{
		ID:      "share",
		Content: []byte("aaa bbb ccc ddd"),
		Metadata: map[string]any{"key": "original"},
	}

	var chunks []*Document
	for chunk := range WithSlidingWindow(doc, 8, 0) {
		chunks = append(chunks, chunk)
	}

	chunks[0].Metadata["key"] = "mutated"

	if doc.Metadata["key"] != "mutated" {
		t.Fatal("expected mutation to propagate to source document")
	}
	if chunks[1].Metadata["key"] != "mutated" {
		t.Fatal("expected mutation to propagate to sibling chunk")
	}
}

func TestWithSlidingWindow_EarlyBreak(t *testing.T) {
	doc := &Document{
		ID:      "break",
		Content: []byte(strings.Repeat("word ", 100)),
	}

	var count int
	for range WithSlidingWindow(doc, 20, 5) {
		count++
		if count == 3 {
			break
		}
	}

	if count != 3 {
		t.Fatalf("expected early stop at 3, got %d", count)
	}
}

func TestWithSlidingWindow_ZeroCopySlice(t *testing.T) {
	content := []byte("hello world foobar")
	doc := &Document{
		ID:       "zero-copy",
		Content:  content,
		Metadata: map[string]any{},
	}

	for chunk := range WithSlidingWindow(doc, 12, 0) {
		chunk.Content[0] = 'H'
		break
	}
	if content[0] != 'H' {
		t.Fatal("chunk is not a zero-copy sub-slice of the original content")
	}
}

func TestWithSlidingWindow_NegativeOverlap(t *testing.T) {
	doc := &Document{
		ID:       "neg",
		Content:  []byte("aaa bbb ccc"),
		Metadata: map[string]any{},
	}

	var count int
	for range WithSlidingWindow(doc, 8, -5) {
		count++
	}
	if count != 2 {
		t.Fatalf("got %d chunks, want 2", count)
	}
}
