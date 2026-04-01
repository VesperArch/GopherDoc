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

func TestWithParagraphs_MetadataIsolation(t *testing.T) {
	doc := &Document{
		ID:      "doc-iso",
		Content: []byte("A\n\nB"),
		Metadata: map[string]any{
			"key":    "original",
			"tags":   []string{"a", "b"},
			"nested": map[string]any{"inner": "value"},
			"items":  []any{1, "two", []byte{0xFF}},
		},
	}

	for chunk := range WithParagraphs(doc) {
		chunk.Metadata["key"] = "mutated"
		chunk.Metadata["tags"].([]string)[0] = "MUTATED"
		chunk.Metadata["nested"].(map[string]any)["inner"] = "MUTATED"
		chunk.Metadata["items"].([]any)[1] = "MUTATED"
		chunk.Metadata["items"].([]any)[2].([]byte)[0] = 0x00
	}

	if doc.Metadata["key"] != "original" {
		t.Fatal("shallow key mutation leaked")
	}
	if doc.Metadata["tags"].([]string)[0] != "a" {
		t.Fatal("[]string mutation leaked into source")
	}
	if doc.Metadata["nested"].(map[string]any)["inner"] != "value" {
		t.Fatal("nested map mutation leaked into source")
	}
	if doc.Metadata["items"].([]any)[1] != "two" {
		t.Fatal("[]any mutation leaked into source")
	}
	if doc.Metadata["items"].([]any)[2].([]byte)[0] != 0xFF {
		t.Fatal("[]byte mutation leaked into source")
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
