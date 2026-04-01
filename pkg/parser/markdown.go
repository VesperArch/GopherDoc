// Package parser implements document.Parser for supported file formats.
package parser

import (
	"context"
	"fmt"
	"io"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

var _ document.Parser = (*MarkdownParser)(nil)

// DefaultMaxBytes is the default read limit applied when MaxBytes is zero.
const DefaultMaxBytes = 10 << 20 // 10 MB

// MarkdownParser converts Markdown sources into Documents. Callers may
// set MaxBytes to cap memory usage; zero means DefaultMaxBytes applies.
type MarkdownParser struct {
	MaxBytes int64
}

// Parse reads the full Markdown content from r into a Document.
// It enforces a byte limit via io.LimitReader to prevent resource
// exhaustion. The caller owns the io.Reader lifetime.
func (p *MarkdownParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("markdown: parse: %w", ctx.Err())
	default:
	}

	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("markdown: read: %w", err)
	}

	return &document.Document{
		Content:  data,
		Metadata: map[string]any{"format": "markdown"},
	}, nil
}
