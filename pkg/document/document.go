// Package document defines data models and ingestion contracts for GopherDoc.
package document

import (
	"context"
	"io"
)

// Document is the normalized output of a parsed source.
//
// Content holds the full document body in memory. For large documents
// (>100 MB) callers should stream through io.Reader at the Parser
// level rather than materializing the entire payload here.
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
