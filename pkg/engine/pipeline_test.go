package engine

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vesperarch/gopherdoc/pkg/parser"
)

func newTestRegistry(maxBytes int64) *parser.Registry {
	reg := parser.NewRegistry()
	_ = reg.Register("md", &parser.MarkdownParser{MaxBytes: maxBytes})
	_ = reg.Register("txt", &parser.PlainTextParser{MaxBytes: maxBytes})
	return reg
}

func readerOpener(s string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(s)), nil
	}
}

func TestPipeline_FanOut(t *testing.T) {
	tests := []struct {
		name       string
		workers    int
		jobs       int
		paragraphs int
		wantChunks int
	}{
		{"5_jobs_2_workers_2_para", 2, 5, 2, 10},
		{"3_jobs_4_workers_1_para", 4, 3, 1, 3},
		{"10_jobs_4_workers_3_para", 4, 10, 3, 30},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewPipeline(newTestRegistry(0), 0, 0)
			ctx := context.Background()
			tasks := make(chan Task, tc.jobs)

			for i := range tc.jobs {
				tasks <- Task{
					ID:   fmt.Sprintf("doc-%d", i),
					Name: fmt.Sprintf("doc-%d.md", i),
					Open: readerOpener(buildBody(tc.paragraphs, i)),
				}
			}
			close(tasks)

			var chunks, errs int
			for r := range p.Run(ctx, tc.workers, tasks) {
				if r.Err != nil {
					errs++
					continue
				}
				chunks++
			}

			if errs != 0 {
				t.Errorf("got %d errors, want 0", errs)
			}
			if chunks != tc.wantChunks {
				t.Errorf("got %d chunks, want %d", chunks, tc.wantChunks)
			}
		})
	}
}

func TestPipeline_MultiFormat(t *testing.T) {
	p := NewPipeline(newTestRegistry(0), 0, 0)
	ctx := context.Background()
	tasks := make(chan Task, 2)

	tasks <- Task{
		ID:   "markdown",
		Name: "readme.md",
		Open: readerOpener("---\ntitle: Hello\n---\nBody."),
	}
	tasks <- Task{
		ID:   "plain",
		Name: "notes.txt",
		Open: readerOpener("Plain text content."),
	}
	close(tasks)

	var docs int
	for r := range p.Run(ctx, 2, tasks) {
		if r.Err != nil {
			t.Fatalf("unexpected error: %v", r.Err)
		}
		docs++
	}

	if docs != 2 {
		t.Errorf("got %d docs, want 2", docs)
	}
}

func TestPipeline_UnsupportedFormat(t *testing.T) {
	p := NewPipeline(newTestRegistry(0), 0, 0)
	ctx := context.Background()
	tasks := make(chan Task, 1)

	tasks <- Task{
		ID:   "unknown",
		Name: "data.csv",
		Open: readerOpener("a,b,c"),
	}
	close(tasks)

	var errs int
	for r := range p.Run(ctx, 1, tasks) {
		if r.Err != nil {
			errs++
			if !strings.Contains(r.Err.Error(), "unsupported format") {
				t.Errorf("unexpected error message: %v", r.Err)
			}
		}
	}

	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestPipeline_MissingExtension(t *testing.T) {
	p := NewPipeline(newTestRegistry(0), 0, 0)
	ctx := context.Background()
	tasks := make(chan Task, 1)

	tasks <- Task{
		ID:   "noext",
		Name: "Makefile",
		Open: readerOpener("all: build"),
	}
	close(tasks)

	var errs int
	for r := range p.Run(ctx, 1, tasks) {
		if r.Err != nil {
			errs++
			if !strings.Contains(r.Err.Error(), "missing file extension") {
				t.Errorf("unexpected error message: %v", r.Err)
			}
		}
	}

	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestPipeline_ErrorPropagation(t *testing.T) {
	p := NewPipeline(newTestRegistry(5), 0, 0)
	ctx := context.Background()
	tasks := make(chan Task, 2)

	tasks <- Task{ID: "good", Name: "good.md", Open: readerOpener("Hi")}
	tasks <- Task{
		ID:   "bad",
		Name: "bad.md",
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(&failReader{}), nil
		},
	}
	close(tasks)

	var docs, errs int
	for r := range p.Run(ctx, 2, tasks) {
		if r.Err != nil {
			errs++
		} else {
			docs++
		}
	}

	if docs != 1 {
		t.Errorf("got %d docs, want 1", docs)
	}
	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestPipeline_OpenError(t *testing.T) {
	p := NewPipeline(newTestRegistry(0), 0, 0)
	ctx := context.Background()
	tasks := make(chan Task, 1)

	tasks <- Task{
		ID:   "broken",
		Name: "broken.md",
		Open: func() (io.ReadCloser, error) {
			return nil, fmt.Errorf("permission denied")
		},
	}
	close(tasks)

	var errs int
	for r := range p.Run(ctx, 1, tasks) {
		if r.Err != nil {
			errs++
		}
	}

	if errs != 1 {
		t.Errorf("got %d errors, want 1", errs)
	}
}

func TestPipeline_CancelDrainsWorkers(t *testing.T) {
	p := NewPipeline(newTestRegistry(0), 0, 0)
	ctx, cancel := context.WithCancel(context.Background())

	tasks := make(chan Task)
	results := p.Run(ctx, 4, tasks)

	cancel()
	close(tasks)

	done := make(chan struct{})
	go func() {
		for range results {
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine leak: pipeline alive 2s after cancel")
	}
}

func TestPipeline_RaceFree(t *testing.T) {
	const numJobs = 100
	p := NewPipeline(newTestRegistry(0), 0, 0)
	ctx := context.Background()
	tasks := make(chan Task, numJobs)

	for i := range numJobs {
		tasks <- Task{
			ID:   fmt.Sprintf("race-%d", i),
			Name: fmt.Sprintf("race-%d.md", i),
			Open: readerOpener("A\n\nB"),
		}
	}
	close(tasks)

	var count atomic.Int64
	for r := range p.Run(ctx, 8, tasks) {
		if r.Err != nil {
			t.Fatalf("unexpected error: %v", r.Err)
		}
		count.Add(1)
	}

	if got := count.Load(); got != 200 {
		t.Fatalf("got %d chunks, want 200", got)
	}
}

func TestPipeline_SlidingWindowChunking(t *testing.T) {
	p := NewPipeline(newTestRegistry(0), 20, 5)
	ctx := context.Background()
	tasks := make(chan Task, 1)

	tasks <- Task{
		ID:   "sw-doc",
		Name: "sw-doc.txt",
		Open: readerOpener("The quick brown fox jumps over the lazy dog"),
	}
	close(tasks)

	var chunks []string
	for r := range p.Run(ctx, 1, tasks) {
		if r.Err != nil {
			t.Fatalf("unexpected error: %v", r.Err)
		}
		chunks = append(chunks, string(r.Doc.Content))
	}

	if len(chunks) < 2 {
		t.Fatalf("expected multiple sliding window chunks, got %d: %v", len(chunks), chunks)
	}

	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1]
		cur := chunks[i]
		overlap := false
		for w := range len(cur) {
			end := w + 3
			if end > len(cur) {
				break
			}
			if strings.Contains(prev, cur[w:end]) {
				overlap = true
				break
			}
		}
		if !overlap {
			t.Errorf("no overlap detected between chunk[%d] and chunk[%d]", i-1, i)
		}
	}
}

func TestPipeline_FallbackToParagraphs(t *testing.T) {
	p := NewPipeline(newTestRegistry(0), 0, 0)
	ctx := context.Background()
	tasks := make(chan Task, 1)

	tasks <- Task{
		ID:   "para-doc",
		Name: "para-doc.md",
		Open: readerOpener("First paragraph.\n\nSecond paragraph.\n\nThird paragraph."),
	}
	close(tasks)

	var count int
	for r := range p.Run(ctx, 1, tasks) {
		if r.Err != nil {
			t.Fatalf("unexpected error: %v", r.Err)
		}
		count++
	}

	if count != 3 {
		t.Fatalf("got %d chunks, want 3 paragraphs", count)
	}
}

func buildBody(paragraphs, seed int) string {
	parts := make([]string, paragraphs)
	for i := range paragraphs {
		parts[i] = fmt.Sprintf("Paragraph %d of doc %d.", i, seed)
	}
	return strings.Join(parts, "\n\n")
}

type failReader struct{}

func (*failReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("simulated I/O failure")
}
