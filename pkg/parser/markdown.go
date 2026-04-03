// Package parser implements document parsing with no external dependencies.
package parser

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/vesperarch/gopherdoc/internal/pool"
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

// Parse reads r and returns a Document with front matter metadata extracted.
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
	body := pool.GetBuffer()
	var lineBuf []byte
	state := stateSeeking

	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("markdown: scan interrupted: %w", err)
		}

		frag, isPrefix, err := br.ReadLine()
		if err == io.EOF {
			if len(lineBuf) > 0 {
				processLine(lineBuf, state, meta, body)
				lineBuf = lineBuf[:0]
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("markdown: read: %w", err)
		}

		if state == stateBody {
			body.Write(frag)
			if !isPrefix {
				body.WriteByte('\n')
			}
			continue
		}

		lineBuf = append(lineBuf, frag...)
		if isPrefix {
			continue
		}

		state = processLine(lineBuf, state, meta, body)
		lineBuf = lineBuf[:0]
	}

	return &document.Document{
		Content:  bytes.TrimSuffix(body.Bytes(), []byte("\n")),
		Metadata: meta,
		PoolBuf:  body,
	}, nil
}

func processLine(line []byte, state parseState, meta map[string]any, body *bytes.Buffer) parseState {
	lineStr := strings.TrimSpace(string(line))

	switch state {
	case stateSeeking:
		if lineStr == frontMatterFence {
			return stateMetadata
		}
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
