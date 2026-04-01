package parser

import (
	"bufio"
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

// Parse reads up to MaxBytes from r using bufio.Reader so that arbitrarily
// long lines never trigger buffer-size errors. Content is accumulated via
// ReadSlice which avoids extra allocations on the common short-line path.
func (p *PlainTextParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("plaintext: %w", err)
	}

	br := bufio.NewReaderSize(io.LimitReader(r, limit), 64<<10)
	var buf bytes.Buffer

	for {
		// ReadSlice returns the internal buffer slice up to the next '\n'.
		// When the line exceeds the internal buffer it returns ErrBufferFull
		// with a partial slice — we write it and loop to get the remainder.
		// This handles blobs of any size without ever hitting a token limit.
		frag, err := br.ReadSlice('\n')
		if len(frag) > 0 {
			buf.Write(frag)
		}
		if err == nil {
			continue
		}
		if err == bufio.ErrBufferFull {
			// Partial fragment — loop to accumulate the rest of this line.
			continue
		}
		if err == io.EOF {
			break
		}
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
