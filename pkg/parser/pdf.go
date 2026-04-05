package parser

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/vesperarch/gopherdoc/internal/pool"
	"github.com/vesperarch/gopherdoc/pkg/document"
)

var _ document.Parser = (*PDFParser)(nil)

const defaultPDFTimeout = 30 * time.Second

// ExecFunc executes a subprocess and returns its stdout output.
type ExecFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// PDFParser extracts text from PDF files via pdftotext (poppler-utils).
// If WithOCR is true and pdftotext yields no text (scanned PDF), it falls
// back to pdftoppm + tesseract for optical character recognition.
//
// Call Available() before registering to verify the system dependency.
type PDFParser struct {
	MaxBytes int64
	Timeout  time.Duration
	WithOCR  bool

	// Exec replaces os/exec in tests and benchmarks. If nil, uses exec.CommandContext.
	Exec ExecFunc
}

// Available returns nil if pdftotext is found in PATH.
func (p *PDFParser) Available() error {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return fmt.Errorf("pdf: pdftotext not found in PATH: %w", err)
	}
	return nil
}

// Parse reads up to MaxBytes from r and returns a Document.
func (p *PDFParser) Parse(ctx context.Context, r io.Reader) (*document.Document, error) {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = defaultPDFTimeout
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}

	tmp, err := os.CreateTemp("", "gopherdoc-pdf-*")
	if err != nil {
		return nil, fmt.Errorf("pdf: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, io.LimitReader(r, limit)); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("pdf: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("pdf: close temp: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	run := p.execFn()
	out, err := run(execCtx, "pdftotext", tmp.Name(), "-")
	if err != nil {
		return nil, fmt.Errorf("pdf: pdftotext: %w", err)
	}

	if len(bytes.TrimSpace(out)) == 0 && p.WithOCR {
		out, err = p.ocrFallback(execCtx, run, tmp.Name())
		if err != nil {
			return nil, fmt.Errorf("pdf: ocr: %w", err)
		}
	}

	if len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("pdf: no text extracted")
	}

	buf := pool.GetBuffer()
	buf.Write(bytes.TrimRight(out, "\n"))

	return &document.Document{
		Content:  buf.Bytes(),
		Metadata: map[string]any{"format": "pdf"},
		PoolBuf:  buf,
	}, nil
}

func (p *PDFParser) execFn() ExecFunc {
	if p.Exec != nil {
		return p.Exec
	}
	return defaultExec
}

func defaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// ocrFallback converts PDF pages to PNG via pdftoppm and runs tesseract on
// each page. Requires pdftoppm and tesseract in PATH.
func (p *PDFParser) ocrFallback(ctx context.Context, run ExecFunc, pdfPath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "gopherdoc-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("create ocr tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pagePrefix := filepath.Join(tmpDir, "page")
	if _, err := run(ctx, "pdftoppm", "-r", "300", "-png", pdfPath, pagePrefix); err != nil {
		return nil, fmt.Errorf("pdftoppm: %w", err)
	}

	pages, err := filepath.Glob(pagePrefix + "*.png")
	if err != nil || len(pages) == 0 {
		return nil, fmt.Errorf("pdftoppm produced no pages")
	}
	sort.Strings(pages)

	var out []byte
	for _, page := range pages {
		text, err := run(ctx, "tesseract", page, "stdout", "-l", "eng")
		if err != nil {
			return nil, fmt.Errorf("tesseract %s: %w", filepath.Base(page), err)
		}
		out = append(out, text...)
	}
	return out, nil
}
