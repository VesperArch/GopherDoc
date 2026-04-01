// Package parser implements high-performance document parsing with zero external dependencies.
package parser

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

var _ document.Parser = (*MarkdownParser)(nil)

const (
	DefaultMaxBytes  = 10 << 20 // 10 MB
	frontMatterFence = "---"
)

type parseState int

const (
	stateSeeking parseState = iota
	stateMetadata
	stateBody
)

// MarkdownParser extracts YAML-like front matter and body content from Markdown.
// It applies a hard memory limit to prevent resource exhaustion during ingestion.
type MarkdownParser struct {
	MaxBytes int64 // MaxBytes caps the reader; defaults to DefaultMaxBytes if <= 0.
}

// Parse processes r into a Document. It identifies front matter by an opening "---"
// on the first line. Content is accumulated via streaming to minimize allocations.
// If the opening fence is never closed, the remaining lines are treated as metadata.
func (p *MarkdownParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}

	// Immediate exit if context is already dead.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("markdown: %w", err)
	}

	scanner := bufio.NewScanner(io.LimitReader(r, limit))
	meta := map[string]any{"format": "markdown"}
	var body bytes.Buffer
	state := stateSeeking

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("markdown: scan interrupted: %w", err)
		}

		line := scanner.Text()

		switch state {
		case stateSeeking:
			if strings.TrimSpace(line) == frontMatterFence {
				state = stateMetadata
				continue
			}
			state = stateBody
			fallthrough // First line wasn't a fence; treat as body.

		case stateMetadata:
			if strings.TrimSpace(line) == frontMatterFence {
				state = stateBody
				continue
			}
			if k, v, ok := parseMetaLine(line); ok {
				meta[k] = v
			}

		case stateBody:
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("markdown: scan: %w", err)
	}

	return &document.Document{
		Content:  bytes.TrimSuffix(body.Bytes(), []byte("\n")),
		Metadata: meta,
	}, nil
}

func parseMetaLine(line string) (string, string, bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:idx])
	v := strings.TrimSpace(line[idx+1:])
	return k, v, k != ""
}