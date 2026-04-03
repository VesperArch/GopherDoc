// Package document defines data models and ingestion contracts for GopherDoc.
package document

import (
	"bytes"
	"context"
	"io"

	"github.com/vesperarch/gopherdoc/internal/pool"
)

// Document is the normalized output of a parsed source.
//
// If PoolBuf is non-nil, its memory backs Content. The consumer of the last
// chunk derived from this Document must call Release when done.
type Document struct {
	ID       string
	Content  []byte
	Metadata map[string]any
	PoolBuf  *bytes.Buffer
}

// Release returns the backing buffer to the internal pool. Call it once,
// after consuming Content from the last chunk emitted by the pipeline.
// It is safe to call on Documents where PoolBuf is nil.
func (d *Document) Release() {
	if d.PoolBuf != nil {
		pool.PutBuffer(d.PoolBuf)
		d.PoolBuf = nil
	}
}

// Parser converts a byte stream into a Document. Implementations must
// respect ctx cancellation and wrap returned errors with %w.
type Parser interface {
	Parse(ctx context.Context, r io.Reader) (*Document, error)
}
