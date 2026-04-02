// Package document defines data models and ingestion contracts for GopherDoc.
package document

import (
	"context"
	"io"
)

// Document is the normalized output of a parsed source.
type Document struct {
	ID       string
	Content  []byte
	Metadata map[string]any
}

// Parser converts a byte stream into a Document. Implementations must
// respect ctx cancellation and wrap returned errors with %w.
type Parser interface {
	Parse(ctx context.Context, r io.Reader) (*Document, error)
}
