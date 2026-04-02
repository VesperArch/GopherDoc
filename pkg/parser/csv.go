package parser

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

var _ document.Parser = (*CSVParser)(nil)

// CSVParser reads tabular data into a Document. Each data row becomes a
// line of comma-separated values in the content. Column headers (first row)
// are stored in metadata under "columns" and omitted from content.
type CSVParser struct {
	MaxBytes int64
	Comma    rune // field delimiter; defaults to ',' if zero
}

// Parse reads up to MaxBytes from r and returns a Document.
func (p *CSVParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("csv: %w", err)
	}

	comma := p.Comma
	if comma == 0 {
		comma = ','
	}

	cr := csv.NewReader(io.LimitReader(r, limit))
	cr.Comma = comma
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1 // allow ragged rows

	meta := map[string]any{"format": "csv"}

	// First row is treated as headers.
	headers, err := cr.Read()
	if err == io.EOF {
		return &document.Document{Content: nil, Metadata: meta}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("csv: read: %w", err)
	}
	meta["columns"] = headers

	var buf bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("csv: %w", err)
		}
		record, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv: read: %w", err)
		}
		buf.WriteString(strings.Join(record, ", "))
		buf.WriteByte('\n')
	}

	return &document.Document{
		Content:  bytes.TrimRight(buf.Bytes(), "\n"),
		Metadata: meta,
	}, nil
}
