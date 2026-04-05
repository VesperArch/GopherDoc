package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"github.com/vesperarch/gopherdoc/pkg/engine"
	"github.com/vesperarch/gopherdoc/pkg/parser"
)

func main() {
	dir := flag.String("dir", ".", "root directory to walk")
	workers := flag.Int("workers", runtime.NumCPU(), "number of concurrent workers")
	limit := flag.Int64("limit", 10<<20, "max bytes per file")
	chunkSize := flag.Int("chunk-size", 0, "sliding window chunk size in bytes (0 = paragraph mode)")
	overlapSize := flag.Int("overlap", 0, "sliding window overlap in bytes")
	ocr := flag.Bool("ocr", false, "enable OCR fallback for scanned PDFs (requires tesseract and pdftoppm)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	reg := parser.NewRegistry()
	_ = reg.Register("md", &parser.MarkdownParser{MaxBytes: *limit})
	_ = reg.Register("txt", &parser.PlainTextParser{MaxBytes: *limit})
	_ = reg.Register("csv", &parser.CSVParser{MaxBytes: *limit})
	_ = reg.Register("json", &parser.JSONParser{MaxBytes: *limit})

	pdfP := &parser.PDFParser{MaxBytes: *limit, WithOCR: *ocr}
	if err := pdfP.Available(); err != nil {
		fmt.Fprintf(os.Stderr, "warn: %v\n", err)
	} else {
		_ = reg.Register("pdf", pdfP)
	}

	tasks := make(chan engine.Task, *workers)
	p := engine.NewPipeline(reg, *chunkSize, *overlapSize)
	results := p.Run(ctx, *workers, tasks)

	go func() {
		defer close(tasks)
		_ = filepath.WalkDir(*dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(os.Stderr, "walk: %s: %v\n", path, err)
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := filepath.Ext(d.Name())
			if ext == "" {
				return nil
			}
			if _, err := reg.Get(ext); err != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case tasks <- engine.Task{
				ID:   path,
				Name: d.Name(),
				Open: func() (io.ReadCloser, error) {
					return os.Open(path)
				},
			}:
			}
			return nil
		})
	}()

	var (
		chunks, errs int
		wg           sync.WaitGroup
		enc          = json.NewEncoder(os.Stdout)
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := range results {
			if r.Err != nil {
				errs++
				fmt.Fprintf(os.Stderr, "error: %v\n", r.Err)
				continue
			}
			chunks++
			if err := enc.Encode(r.Doc); err != nil {
				log.Fatalf("encode: %v", err)
			}
			r.Doc.Release()
		}
	}()

	wg.Wait()

	fmt.Fprintf(os.Stderr, "done: %d chunks, %d errors\n", chunks, errs)
	if errs > 0 {
		os.Exit(1)
	}
}
