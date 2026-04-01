package engine

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/vesperarch/gopherdoc/pkg/document"
	"github.com/vesperarch/gopherdoc/pkg/parser"
)

// Task is a unit of work fed into a Pipeline.
type Task struct {
	ID       string
	Reader   io.Reader
	Metadata map[string]any
}

// Result carries either a successfully chunked Document or an error.
// Exactly one of Doc or Err is non-zero.
type Result struct {
	Doc *document.Document
	Err error
}

// Pipeline orchestrates concurrent document ingestion: each worker
// parses an Task through MarkdownParser, splits it with
// WithParagraphs, and emits one Result per chunk. The returned channel
// is closed after all workers drain the input channel and finish.
type Pipeline struct {
	Parser *parser.MarkdownParser
}

// Run fans out numWorkers goroutines that consume from inputs and emit
// Results. The caller must close inputs when done submitting. The
// returned channel is safe to range over.
func (p *Pipeline) Run(ctx context.Context, numWorkers int, inputs <-chan Task) <-chan Result {
	out := make(chan Result, numWorkers)
	var wg sync.WaitGroup

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.worker(ctx, inputs, out)
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

func (p *Pipeline) worker(ctx context.Context, inputs <-chan Task, out chan<- Result) {
	for {
		select {
		case <-ctx.Done():
			return
		case in, ok := <-inputs:
			if !ok {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			p.process(ctx, in, out)
		}
	}
}

func (p *Pipeline) process(ctx context.Context, in Task, out chan<- Result) {
	doc, err := p.Parser.Parse(ctx, in.Reader)
	if err != nil {
		p.emit(ctx, out, Result{Err: fmt.Errorf("pipeline: %s: %w", in.ID, err)})
		return
	}

	doc.ID = in.ID
	if in.Metadata != nil {
		for k, v := range in.Metadata {
			doc.Metadata[k] = v
		}
	}

	for chunk := range document.WithParagraphs(doc) {
		if ctx.Err() != nil {
			return
		}
		p.emit(ctx, out, Result{Doc: chunk})
	}
}

func (p *Pipeline) emit(ctx context.Context, out chan<- Result, r Result) {
	select {
	case <-ctx.Done():
	case out <- r:
	}
}
