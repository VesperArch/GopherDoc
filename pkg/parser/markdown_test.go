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
		name    string
		input   string
		max     int64
		wantLen int
		wantErr bool
	}{
		{
			name:    "happy_path",
			input:   "# Title\n\nBody text.",
			wantLen: 19,
		},
		{
			name:    "empty_input",
			input:   "",
			wantLen: 0,
		},
		{
			name:    "truncated_by_limit",
			input:   strings.Repeat("a", 100),
			max:     10,
			wantLen: 10,
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
			if got := len(doc.Content); got != tc.wantLen {
				t.Errorf("content length = %d, want %d", got, tc.wantLen)
			}
			if doc.Metadata["format"] != "markdown" {
				t.Errorf("metadata format = %v, want markdown", doc.Metadata["format"])
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
