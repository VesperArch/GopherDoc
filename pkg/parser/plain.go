package parser

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/vesperarch/gopherdoc/internal/pool"
	"github.com/vesperarch/gopherdoc/pkg/document"
)

var _ document.Parser = (*PlainTextParser)(nil)

// PlainTextParser reads raw text from an io.Reader into a Document with
// no structural interpretation. It applies the same memory limit pattern
// as MarkdownParser to prevent resource exhaustion.
type PlainTextParser struct {
	MaxBytes int64
}

// Parse reads up to MaxBytes from r and returns a Document.
func (p *PlainTextParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("plaintext: %w", err)
	}

	br := bufio.NewReaderSize(io.LimitReader(r, limit), 64<<10)
	buf := pool.GetBuffer()

	for {
		frag, err := br.ReadSlice('\n')
		if len(frag) > 0 {
			buf.Write(frag)
		}
		if err == nil || err == bufio.ErrBufferFull {
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
		PoolBuf:  buf,
	}, nil
}
