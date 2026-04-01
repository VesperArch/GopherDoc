package parser

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

var _ document.Parser = (*PlainTextParser)(nil)

// PlainTextParser reads raw text from an io.Reader into a Document with
// no structural interpretation. It applies the same memory limit pattern
// as MarkdownParser to prevent resource exhaustion.
type PlainTextParser struct {
	MaxBytes int64
}

// Parse reads up to MaxBytes from r and returns a Document whose Content
// is the raw text and whose Metadata marks the format as "plaintext".
func (p *PlainTextParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("plaintext: %w", err)
	}

	var buf bytes.Buffer
	_, err := io.Copy(&buf, io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("plaintext: read: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("plaintext: %w", err)
	}

	return &document.Document{
		Content:  bytes.TrimRight(buf.Bytes(), "\n"),
		Metadata: map[string]any{"format": "plaintext"},
	}, nil
}
