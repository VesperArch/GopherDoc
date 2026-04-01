package engine

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/vesperarch/gopherdoc/pkg/document"
	"github.com/vesperarch/gopherdoc/pkg/parser"
)

// Task describes a document to be ingested. Open is invoked by the
// worker at processing time rather than at submission time, so file
// descriptors are not held while the task sits in a channel buffer.
type Task struct {
	ID       string
	Open     func() (io.ReadCloser, error)
	Metadata map[string]any
}

// Result is the output of a single pipeline stage. Exactly one of Doc
// or Err is non-nil.
type Result struct {
	Doc *document.Document
	Err error
}

// Pipeline coordinates parse-then-chunk ingestion across a bounded
// worker pool. The returned Result channel is closed only after every
// worker has drained the input channel and finished emitting, so
// ranging over it is safe.
type Pipeline struct {
	Parser *parser.MarkdownParser
}

// Run starts numWorkers goroutines consuming from tasks and returns a
// channel of Results. The caller must close tasks when done submitting;
// the result channel closes itself once all workers exit.
func (p *Pipeline) Run(ctx context.Context, numWorkers int, tasks <-chan Task) <-chan Result {
	out := make(chan Result, numWorkers)
	var wg sync.WaitGroup

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.worker(ctx, tasks, out)
		}()
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

func (p *Pipeline) worker(ctx context.Context, tasks <-chan Task, out chan<- Result) {
	for {
		select {
		case <-ctx.Done():
			return
		case in, ok := <-tasks:
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
	rc, err := in.Open()
	if err != nil {
		p.emit(ctx, out, Result{Err: fmt.Errorf("pipeline: open %s: %w", in.ID, err)})
		return
	}
	defer rc.Close()

	doc, err := p.Parser.Parse(ctx, rc)
	if err != nil {
		p.emit(ctx, out, Result{Err: fmt.Errorf("pipeline: parse %s: %w", in.ID, err)})
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
