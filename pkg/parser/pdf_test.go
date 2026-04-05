package parser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/vesperarch/gopherdoc/pkg/document"
)

func TestPDFParser_ImplementsInterface(t *testing.T) {
	var _ document.Parser = (*PDFParser)(nil)
}

func TestPDFParser_Parse(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		max         int64
		exec        ExecFunc
		wantContent string
		wantErr     string
	}{
		{
			name:  "happy_path",
			input: "%PDF-1.4 fake bytes",
			exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				return []byte("Hello, GopherDoc!\n"), nil
			},
			wantContent: "Hello, GopherDoc!",
		},
		{
			name:  "multipage_text",
			input: "%PDF-1.4 fake bytes",
			exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				return []byte("Page one content.\nPage two content.\n"), nil
			},
			wantContent: "Page one content.\nPage two content.",
		},
		{
			name:  "trailing_newlines_trimmed",
			input: "%PDF-1.4 fake bytes",
			exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				return []byte("Some text.\n\n\n"), nil
			},
			wantContent: "Some text.",
		},
		{
			name:  "empty_output_no_ocr",
			input: "%PDF-1.4 fake bytes",
			exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				return []byte(""), nil
			},
			wantErr: "pdf: no text extracted",
		},
		{
			name:  "whitespace_only_output",
			input: "%PDF-1.4 fake bytes",
			exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				return []byte("   \n\n   "), nil
			},
			wantErr: "pdf: no text extracted",
		},
		{
			name:  "pdftotext_fails",
			input: "%PDF-1.4 fake bytes",
			exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				return nil, errors.New("exit status 1")
			},
			wantErr: "pdf: pdftotext:",
		},
		{
			name:  "truncated_by_limit",
			input: strings.Repeat("x", 1000),
			max:   10,
			exec: func(_ context.Context, name string, args ...string) ([]byte, error) {
				// args[0] is the temp file path — verify its size is capped
				if name == "pdftotext" {
					info, err := os.Stat(args[0])
					if err != nil {
						return nil, err
					}
					if info.Size() > 10 {
						return nil, errors.New("temp file exceeds MaxBytes limit")
					}
				}
				return []byte("truncated content"), nil
			},
			wantContent: "truncated content",
		},
		{
			name:  "metadata_format_is_pdf",
			input: "%PDF-1.4 fake bytes",
			exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				return []byte("text content"), nil
			},
			wantContent: "text content",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &PDFParser{MaxBytes: tc.max, Exec: tc.exec}
			doc, err := p.Parse(context.Background(), strings.NewReader(tc.input))

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := string(doc.Content); got != tc.wantContent {
				t.Errorf("content = %q, want %q", got, tc.wantContent)
			}
			if doc.Metadata["format"] != "pdf" {
				t.Errorf("format = %v, want pdf", doc.Metadata["format"])
			}
			if doc.PoolBuf == nil {
				t.Error("PoolBuf is nil")
			}
			doc.Release()
		})
	}
}

func TestPDFParser_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &PDFParser{Exec: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("should not be called"), nil
	}}
	_, err := p.Parse(ctx, strings.NewReader("%PDF-1.4"))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestPDFParser_OCRFallback(t *testing.T) {
	p := &PDFParser{
		WithOCR: true,
		Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "pdftotext":
				return []byte(""), nil // scanned PDF — triggers OCR
			case "pdftoppm":
				pagePrefix := args[len(args)-1]
				for _, suffix := range []string{"-1.png", "-2.png"} {
					if err := os.WriteFile(pagePrefix+suffix, []byte{}, 0600); err != nil {
						return nil, err
					}
				}
				return nil, nil
			case "tesseract":
				return []byte("OCR text from " + filepath.Base(args[0]) + "\n"), nil
			default:
				return nil, errors.New("unexpected command: " + name)
			}
		},
	}

	doc, err := p.Parse(context.Background(), strings.NewReader("%PDF-1.4"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := string(doc.Content)
	if !strings.Contains(content, "OCR text from") {
		t.Errorf("expected OCR content, got %q", content)
	}
	if doc.Metadata["format"] != "pdf" {
		t.Errorf("format = %v, want pdf", doc.Metadata["format"])
	}
	doc.Release()
}

func TestPDFParser_OCRFallback_pdftoppmFails(t *testing.T) {
	p := &PDFParser{
		WithOCR: true,
		Exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "pdftotext" {
				return []byte(""), nil
			}
			return nil, errors.New("pdftoppm: command not found")
		},
	}

	_, err := p.Parse(context.Background(), strings.NewReader("%PDF-1.4"))
	if err == nil {
		t.Fatal("expected error when pdftoppm fails")
	}
	if !strings.Contains(err.Error(), "pdf: ocr:") {
		t.Errorf("error = %q, want it to contain \"pdf: ocr:\"", err.Error())
	}
}

func TestPDFParser_TempfileCleanup(t *testing.T) {
	const concurrency = 30

	p := &PDFParser{
		Exec: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "pdftotext" {
				return []byte("extracted text content for cleanup test"), nil
			}
			return nil, errors.New("unexpected: " + name)
		},
	}

	before := countTempFiles(t, "gopherdoc-pdf-")

	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doc, err := p.Parse(context.Background(), strings.NewReader("%PDF-1.4"))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			doc.Release()
		}()
	}
	wg.Wait()
	runtime.GC()

	after := countTempFiles(t, "gopherdoc-pdf-")
	if after > before {
		t.Errorf("tempfile leak: %d before, %d after (%d leaked)", before, after, after-before)
	}
}

func countTempFiles(t *testing.T, prefix string) int {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read tempdir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			n++
		}
	}
	return n
}

func TestPDFParser_Available(t *testing.T) {
	p := &PDFParser{}
	err := p.Available()
	if err != nil {
		t.Skipf("pdftotext not installed: %v", err)
	}
}
