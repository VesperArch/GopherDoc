package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

func TestCSVParser_ImplementsInterface(t *testing.T) {
	var _ document.Parser = (*CSVParser)(nil)
}

func TestCSVParser_Parse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		comma       rune
		max         int64
		wantContent string
		wantColumns []string
		wantErr     bool
	}{
		{
			name:        "basic",
			input:       "name,age,city\nAlice,30,NYC\nBob,25,LA",
			wantContent: "Alice, 30, NYC\nBob, 25, LA",
			wantColumns: []string{"name", "age", "city"},
		},
		{
			name:        "tsv_via_comma_override",
			input:       "name\tage\nAlice\t30",
			comma:       '\t',
			wantContent: "Alice, 30",
			wantColumns: []string{"name", "age"},
		},
		{
			name:        "headers_only_no_data",
			input:       "col1,col2,col3",
			wantContent: "",
			wantColumns: []string{"col1", "col2", "col3"},
		},
		{
			name:        "empty_input",
			input:       "",
			wantContent: "",
			wantColumns: nil,
		},
		{
			name:        "ragged_rows",
			input:       "a,b,c\n1,2\n3,4,5,6",
			wantContent: "1, 2\n3, 4, 5, 6",
			wantColumns: []string{"a", "b", "c"},
		},
		{
			name:        "leading_spaces_trimmed",
			input:       "x,y\n  hello,  world",
			wantContent: "hello, world",
			wantColumns: []string{"x", "y"},
		},
		{
			name:        "truncated_by_limit",
			input:       "a,b\n" + strings.Repeat("x,y\n", 1000),
			max:         10,
			wantErr:     false, // limit cuts input; partial read is still valid CSV
			wantContent: "x, y\nx, ",
			wantColumns: []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &CSVParser{MaxBytes: tc.max, Comma: tc.comma}
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
			if doc.Metadata["format"] != "csv" {
				t.Errorf("format = %v, want csv", doc.Metadata["format"])
			}
			if tc.wantColumns != nil {
				cols, ok := doc.Metadata["columns"].([]string)
				if !ok {
					t.Fatal("columns metadata missing or wrong type")
				}
				if len(cols) != len(tc.wantColumns) {
					t.Fatalf("columns = %v, want %v", cols, tc.wantColumns)
				}
				for i, c := range cols {
					if c != tc.wantColumns[i] {
						t.Errorf("column[%d] = %q, want %q", i, c, tc.wantColumns[i])
					}
				}
			}
		})
	}
}

func TestCSVParser_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &CSVParser{}
	_, err := p.Parse(ctx, strings.NewReader("a,b\n1,2"))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
