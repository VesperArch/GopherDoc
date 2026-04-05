//go:build integration

package parser

import (
	"context"
	"os"
	"strings"
	"testing"
)

const samplePDFPath = "testdata/sample.pdf"

func TestPDFParser_Integration_HappyPath(t *testing.T) {
	p := &PDFParser{MaxBytes: DefaultMaxBytes}
	if err := p.Available(); err != nil {
		t.Skipf("pdftotext not available: %v", err)
	}

	f, err := os.Open(samplePDFPath)
	if err != nil {
		t.Skipf("testdata not found: %v — run: go run testdata/gen_sample_pdf.go", err)
	}
	defer f.Close()

	doc, err := p.Parse(context.Background(), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer doc.Release()

	content := string(doc.Content)
	if !strings.Contains(content, "Hello GopherDoc") {
		t.Errorf("expected \"Hello GopherDoc\" in content, got %q", content)
	}
	if doc.Metadata["format"] != "pdf" {
		t.Errorf("format = %v, want pdf", doc.Metadata["format"])
	}
	if doc.PoolBuf == nil {
		t.Error("PoolBuf must be non-nil")
	}
}

func TestPDFParser_Integration_Limit(t *testing.T) {
	p := &PDFParser{MaxBytes: 4}
	if err := p.Available(); err != nil {
		t.Skipf("pdftotext not available: %v", err)
	}

	f, err := os.Open(samplePDFPath)
	if err != nil {
		t.Skipf("testdata not found: %v", err)
	}
	defer f.Close()

	doc, err := p.Parse(context.Background(), f)
	if err != nil {
		return // truncated PDF is invalid — expected
	}
	if doc != nil {
		doc.Release()
	}
}

func TestPDFParser_Integration_CancelledContext(t *testing.T) {
	p := &PDFParser{}
	if err := p.Available(); err != nil {
		t.Skipf("pdftotext not available: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Parse(ctx, strings.NewReader("%PDF-1.4"))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
