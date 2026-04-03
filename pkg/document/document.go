// Package document defines data models and ingestion contracts for GopherDoc.
package document

import (
	"bytes"
	"context"
	"io"
)

// Document is the normalized output of a parsed source.
//
// If PoolBuf is non-nil, its memory backs Content. The consumer of the last
// chunk derived from this Document must call pool.PutBuffer(PoolBuf).
type Document struct {
	ID       string
	Content  []byte
	Metadata map[string]any
	PoolBuf  *bytes.Buffer
}

// Parser converts a byte stream into a Document. Implementations must
// respect ctx cancellation and wrap returned errors with %w.
type Parser interface {
	Parse(ctx context.Context, r io.Reader) (*Document, error)
}
