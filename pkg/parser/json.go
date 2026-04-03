package parser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/vesperarch/gopherdoc/internal/pool"
	"github.com/vesperarch/gopherdoc/pkg/document"
)

var _ document.Parser = (*JSONParser)(nil)

// JSONParser validates and normalises JSON into a Document. Content is the
// pretty-printed form of the input; malformed JSON returns an error.
type JSONParser struct {
	MaxBytes int64
}

// Parse reads up to MaxBytes from r and returns a Document.
func (p *JSONParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("json: read: %w", err)
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("json: parse: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}

	buf := pool.GetBuffer()
	enc := json.NewEncoder(buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("json: marshal: %w", err)
	}

	return &document.Document{
		Content:  bytes.TrimRight(buf.Bytes(), "\n"),
		Metadata: map[string]any{"format": "json"},
		PoolBuf:  buf,
	}, nil
}
