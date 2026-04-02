package document

import "testing"

func TestWithParagraphs(t *testing.T) {
	tests := []struct {
		name       string
		doc        *Document
		wantChunks int
		wantBodies []string
	}{
		{
			name: "three_paragraphs",
			doc: &Document{
				ID:       "doc-1",
				Content:  []byte("First paragraph.\n\nSecond paragraph.\n\nThird paragraph."),
				Metadata: map[string]any{"format": "markdown"},
			},
			wantChunks: 3,
			wantBodies: []string{"First paragraph.", "Second paragraph.", "Third paragraph."},
		},
		{
			name: "single_block",
			doc: &Document{
				ID:       "doc-2",
				Content:  []byte("No paragraph breaks here."),
				Metadata: map[string]any{"format": "markdown"},
			},
			wantChunks: 1,
			wantBodies: []string{"No paragraph breaks here."},
		},
		{
			name: "empty_content",
			doc: &Document{
				ID:       "doc-3",
				Content:  []byte(""),
				Metadata: map[string]any{"format": "markdown"},
			},
			wantChunks: 0,
		},
		{
			name: "whitespace_paragraphs_skipped",
			doc: &Document{
				ID:       "doc-4",
				Content:  []byte("Real content.\n\n   \n\nMore content."),
				Metadata: map[string]any{"format": "markdown"},
			},
			wantChunks: 2,
			wantBodies: []string{"Real content.", "More content."},
		},
		{
			name: "trailing_double_newline",
			doc: &Document{
				ID:      "doc-5",
				Content: []byte("Only one.\n\n"),
			},
			wantChunks: 1,
			wantBodies: []string{"Only one."},
		},
		{
			name: "multiple_consecutive_separators",
			doc: &Document{
				ID:      "doc-6",
				Content: []byte("A\n\n\n\nB"),
			},
			wantChunks: 2,
			wantBodies: []string{"A", "B"},
		},
		{
			name: "only_newlines",
			doc: &Document{
				ID:      "doc-7",
				Content: []byte("\n\n\n\n"),
			},
			wantChunks: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var chunks []*Document
			for chunk := range WithParagraphs(tc.doc) {
				chunks = append(chunks, chunk)
			}

			if got := len(chunks); got != tc.wantChunks {
				t.Fatalf("got %d chunks, want %d", got, tc.wantChunks)
			}
			for i, chunk := range chunks {
				if got := string(chunk.Content); got != tc.wantBodies[i] {
					t.Errorf("chunk[%d] = %q, want %q", i, got, tc.wantBodies[i])
				}
				if chunk.Metadata["format"] != tc.doc.Metadata["format"] {
					t.Errorf("chunk[%d] metadata not inherited", i)
				}
			}
		})
	}
}

func TestWithParagraphs_MetadataSharing(t *testing.T) {
	// Chunks share doc.Metadata by reference. Mutations to chunk.Metadata
	// propagate to the source document and to all sibling chunks.
	// Callers that need per-chunk isolation must copy before mutating.
	doc := &Document{
		ID:      "doc-share",
		Content: []byte("A\n\nB"),
		Metadata: map[string]any{"key": "original"},
	}

	var chunks []*Document
	for chunk := range WithParagraphs(doc) {
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

func TestWithParagraphs_EarlyBreak(t *testing.T) {
	doc := &Document{
		ID:      "doc-break",
		Content: []byte("A\n\nB\n\nC\n\nD"),
	}

	var count int
	for range WithParagraphs(doc) {
		count++
		if count == 2 {
			break
		}
	}

	if count != 2 {
		t.Fatalf("expected early stop at 2, got %d", count)
	}
}

func TestWithParagraphs_ZeroCopy(t *testing.T) {
	content := []byte("hello\n\nworld")
	doc := &Document{ID: "zc", Content: content}

	for chunk := range WithParagraphs(doc) {
		chunk.Content[0] = 'H'
		break
	}

	if content[0] != 'H' {
		t.Fatal("chunk is not a zero-copy sub-slice of the original content")
	}
}

// BenchmarkWithParagraphs_Allocs measures allocations per paragraph emitted.
// With the lazy implementation the only allocation per chunk is the Document
// struct itself — the content slice is a zero-copy view of the source.
func BenchmarkWithParagraphs_Allocs(b *testing.B) {
	const paragraphs = 1000
	var buf []byte
	for range paragraphs {
		buf = append(buf, []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit.\n\n")...)
	}
	doc := &Document{ID: "bench", Content: buf, Metadata: map[string]any{"format": "markdown"}}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		for range WithParagraphs(doc) {
		}
	}
}
