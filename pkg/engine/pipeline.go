package engine

import (
	"context"
	"fmt"
	"io"
	"iter"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vesperarch/gopherdoc/pkg/document"
	"github.com/vesperarch/gopherdoc/pkg/parser"
)

// Task describes a document to be ingested. Open is invoked by the
// worker at processing time rather than at submission time, so file
// descriptors are not held while the task sits in a channel buffer.
type Task struct {
	ID       string
	Name     string
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
// worker pool. Parser resolution is delegated to the injected Registry,
// making the pipeline agnostic to specific file formats.
//
// ChunkSize and OverlapSize control the sliding window chunker. When
// ChunkSize is zero or negative the pipeline falls back to paragraph
// splitting (WithParagraphs).
type Pipeline struct {
	ChunkSize   int
	OverlapSize int
	registry    *parser.Registry
}

// NewPipeline returns a Pipeline that resolves parsers through reg and
// emits chunks according to the given sliding window parameters.
func NewPipeline(reg *parser.Registry, chunkSize, overlapSize int) *Pipeline {
	return &Pipeline{
		ChunkSize:   chunkSize,
		OverlapSize: overlapSize,
		registry:    reg,
	}
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
	ext := strings.TrimPrefix(filepath.Ext(in.Name), ".")
	if ext == "" {
		p.emit(ctx, out, Result{Err: fmt.Errorf("pipeline: %s: missing file extension", in.ID)})
		return
	}

	pr, err := p.registry.Get(ext)
	if err != nil {
		p.emit(ctx, out, Result{Err: fmt.Errorf("pipeline: %s: unsupported format %q", in.ID, ext)})
		return
	}

	rc, err := in.Open()
	if err != nil {
		p.emit(ctx, out, Result{Err: fmt.Errorf("pipeline: open %s: %w", in.ID, err)})
		return
	}
	defer rc.Close()

	doc, err := pr.Parse(ctx, rc)
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

	var chunks iter.Seq[*document.Document]
	if p.ChunkSize > 0 {
		chunks = document.WithSlidingWindow(doc, p.ChunkSize, p.OverlapSize)
	} else {
		chunks = document.WithParagraphs(doc)
	}

	for chunk := range chunks {
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
