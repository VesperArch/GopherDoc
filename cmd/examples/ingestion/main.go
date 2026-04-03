package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/vesperarch/gopherdoc/pkg/engine"
	"github.com/vesperarch/gopherdoc/pkg/parser"
)

func main() {
	const (
		chunkSize   = 4 << 10 // 4 KB
		overlapSize = 512
		numWorkers  = 4
	)

	reg := parser.NewRegistry()
	_ = reg.Register("md", &parser.MarkdownParser{MaxBytes: 10 << 20})
	_ = reg.Register("txt", &parser.PlainTextParser{MaxBytes: 10 << 20})
	_ = reg.Register("csv", &parser.CSVParser{MaxBytes: 10 << 20})
	_ = reg.Register("json", &parser.JSONParser{MaxBytes: 10 << 20})

	p := engine.NewPipeline(reg, chunkSize, overlapSize)
	ctx := context.Background()

	tasks := make(chan engine.Task, numWorkers*2)
	results := p.Run(ctx, numWorkers, tasks)

	go func() {
		defer close(tasks)

		docs := []struct {
			id      string
			name    string
			content string
		}{
			{"doc-1", "article.md", "# Hello\n\nFirst paragraph.\n\nSecond paragraph."},
			{"doc-2", "notes.txt", "Plain text document.\n\nAnother section here."},
		}

		for _, d := range docs {
			d := d
			tasks <- engine.Task{
				ID:   d.id,
				Name: d.name,
				Open: func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(d.content)), nil
				},
			}
		}
	}()

	var chunks, errs int
	for r := range results {
		if r.Err != nil {
			errs++
			fmt.Fprintf(os.Stderr, "error: %v\n", r.Err)
			continue
		}
		chunks++
		fmt.Printf("chunk %s (%d bytes): %q\n", r.Doc.ID, len(r.Doc.Content), r.Doc.Content)
		r.Doc.Release()
	}

	if errs > 0 {
		log.Fatalf("done: %d chunks, %d errors", chunks, errs)
	}
	fmt.Printf("done: %d chunks\n", chunks)
}
