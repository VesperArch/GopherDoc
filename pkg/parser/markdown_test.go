package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

func TestMarkdownParser_ImplementsInterface(t *testing.T) {
	var _ document.Parser = (*MarkdownParser)(nil)
}

func TestMarkdownParser_Parse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int64
		wantBody string
		wantMeta map[string]any
		wantErr  bool
	}{
		{
			name:     "happy_path",
			input:    "# Title\n\nBody text.",
			wantBody: "# Title\n\nBody text.",
		},
		{
			name:     "empty_input",
			input:    "",
			wantBody: "",
		},
		{
			name:     "truncated_by_limit",
			input:    strings.Repeat("a", 100),
			max:      10,
			wantBody: strings.Repeat("a", 10),
		},
		{
			name:  "with_front_matter",
			input: "---\ntitle: Hello World\nauthor: GopherDoc\n---\n# Heading\n\nParagraph.",
			wantBody: "# Heading\n\nParagraph.",
			wantMeta: map[string]any{
				"title":  "Hello World",
				"author": "GopherDoc",
			},
		},
		{
			name:     "no_front_matter",
			input:    "Just plain text.\nSecond line.",
			wantBody: "Just plain text.\nSecond line.",
		},
		{
			name:  "malformed_front_matter",
			input: "---\ntitle: Valid\nbroken line without colon\n---\nBody here.",
			wantBody: "Body here.",
			wantMeta: map[string]any{
				"title": "Valid",
			},
		},
		{
			name:  "empty_body",
			input: "---\ntag: value\n---",
			wantBody: "",
			wantMeta: map[string]any{
				"tag": "value",
			},
		},
		{
			name:  "unclosed_front_matter",
			input: "---\nkey: val\norphan line",
			wantBody: "",
			wantMeta: map[string]any{
				"key": "val",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &MarkdownParser{MaxBytes: tc.max}
			doc, err := p.Parse(context.Background(), strings.NewReader(tc.input))

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := string(doc.Content); got != tc.wantBody {
				t.Errorf("content = %q, want %q", got, tc.wantBody)
			}
			if doc.Metadata["format"] != "markdown" {
				t.Errorf("metadata format = %v, want markdown", doc.Metadata["format"])
			}
			for k, want := range tc.wantMeta {
				if got := doc.Metadata[k]; got != want {
					t.Errorf("metadata[%q] = %v, want %v", k, got, want)
				}
			}
		})
	}
}

func TestMarkdownParser_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &MarkdownParser{}
	_, err := p.Parse(ctx, strings.NewReader("# test"))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
