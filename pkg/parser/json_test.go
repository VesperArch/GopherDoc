package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

func TestJSONParser_ImplementsInterface(t *testing.T) {
	var _ document.Parser = (*JSONParser)(nil)
}

func TestJSONParser_Parse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		max         int64
		wantContent string
		wantErr     bool
	}{
		{
			name:  "object",
			input: `{"name":"Alice","age":30}`,
			wantContent: `{
  "age": 30,
  "name": "Alice"
}`,
		},
		{
			name:  "array",
			input: `[1,2,3]`,
			wantContent: `[
  1,
  2,
  3
]`,
		},
		{
			name:  "nested",
			input: `{"a":{"b":1}}`,
			wantContent: `{
  "a": {
    "b": 1
  }
}`,
		},
		{
			name:        "already_pretty",
			input:       "{\n  \"x\": 1\n}",
			wantContent: "{\n  \"x\": 1\n}",
		},
		{
			name:    "invalid_json",
			input:   `{not valid}`,
			wantErr: true,
		},
		{
			name:    "empty_input",
			input:   ``,
			wantErr: true,
		},
		{
			name:    "truncated_by_limit",
			input:   `{"key":"` + strings.Repeat("x", 100) + `"}`,
			max:     10,
			wantErr: true, // truncated input is invalid JSON
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &JSONParser{MaxBytes: tc.max}
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
			if got := string(doc.Content); got != tc.wantContent {
				t.Errorf("content = %q, want %q", got, tc.wantContent)
			}
			if doc.Metadata["format"] != "json" {
				t.Errorf("format = %v, want json", doc.Metadata["format"])
			}
		})
	}
}

func TestJSONParser_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &JSONParser{}
	_, err := p.Parse(ctx, strings.NewReader(`{"x":1}`))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
