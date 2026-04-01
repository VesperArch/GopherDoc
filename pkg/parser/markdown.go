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

// Parse processes r into a Document using bufio.Reader so that arbitrarily
// long lines (blobs without newlines) never trigger a "token too long" error.
// Lines wider than the internal buffer are accumulated in fragments until a
// newline is found or EOF is reached.
func (p *MarkdownParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("markdown: %w", err)
	}

	br := bufio.NewReaderSize(io.LimitReader(r, limit), 64<<10)
	meta := map[string]any{"format": "markdown"}
	var body bytes.Buffer

	// lineBuf accumulates fragments for lines in seeking/metadata states.
	// Body lines are written directly to body to avoid the extra allocation.
	var lineBuf []byte
	state := stateSeeking

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("markdown: scan interrupted: %w", err)
		}

		frag, isPrefix, err := br.ReadLine()
		if err == io.EOF {
			// Process any partial line already accumulated before exiting.
			if len(lineBuf) > 0 {
				processLine(lineBuf, state, meta, &body)
				lineBuf = lineBuf[:0]
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("markdown: read: %w", err)
		}

		if state == stateBody {
			// Body lines: write fragments directly into body, never allocating
			// a temporary string. isPrefix fragments are written as they arrive.
			body.Write(frag)
			if !isPrefix {
				body.WriteByte('\n')
			}
			continue
		}

		// Seeking / metadata: must see the complete line to inspect it.
		lineBuf = append(lineBuf, frag...)
		if isPrefix {
			continue // more fragments coming for this line
		}

		state = processLine(lineBuf, state, meta, &body)
		lineBuf = lineBuf[:0]
	}

	return &document.Document{
		Content:  bytes.TrimSuffix(body.Bytes(), []byte("\n")),
		Metadata: meta,
	}, nil
}

// processLine dispatches a complete line according to the current state and
// returns the (possibly updated) state.
func processLine(line []byte, state parseState, meta map[string]any, body *bytes.Buffer) parseState {
	lineStr := strings.TrimSpace(string(line))

	switch state {
	case stateSeeking:
		if lineStr == frontMatterFence {
			return stateMetadata
		}
		// Not a front-matter fence: treat as the first body line.
		body.Write(line)
		body.WriteByte('\n')
		return stateBody

	case stateMetadata:
		if lineStr == frontMatterFence {
			return stateBody
		}
		if k, v, ok := parseMetaLine(string(line)); ok {
			meta[k] = v
		}
		return stateMetadata
	}

	return state
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
