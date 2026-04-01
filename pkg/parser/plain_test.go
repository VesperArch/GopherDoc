package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

func TestPlainTextParser_ImplementsInterface(t *testing.T) {
	var _ document.Parser = (*PlainTextParser)(nil)
}

func TestPlainTextParser_Parse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int64
		wantBody string
	}{
		{
			name:     "simple_text",
			input:    "Hello, world!",
			wantBody: "Hello, world!",
		},
		{
			name:     "empty_input",
			input:    "",
			wantBody: "",
		},
		{
			name:     "multiline",
			input:    "Line one.\nLine two.\nLine three.",
			wantBody: "Line one.\nLine two.\nLine three.",
		},
		{
			name:     "trailing_newline_trimmed",
			input:    "Content here.\n\n",
			wantBody: "Content here.",
		},
		{
			name:     "truncated_by_limit",
			input:    strings.Repeat("x", 200),
			max:      50,
			wantBody: strings.Repeat("x", 50),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &PlainTextParser{MaxBytes: tc.max}
			doc, err := p.Parse(context.Background(), strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := string(doc.Content); got != tc.wantBody {
				t.Errorf("content = %q, want %q", got, tc.wantBody)
			}
			if doc.Metadata["format"] != "plaintext" {
				t.Errorf("format = %v, want plaintext", doc.Metadata["format"])
			}
		})
	}
}

func TestPlainTextParser_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &PlainTextParser{}
	_, err := p.Parse(ctx, strings.NewReader("test"))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
