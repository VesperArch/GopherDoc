package engine

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vesperarch/gopherdoc/pkg/parser"
)

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
			p := &Pipeline{Parser: &parser.MarkdownParser{}}
			ctx := context.Background()
			inputs := make(chan Task, tc.jobs)

			for i := range tc.jobs {
				body := buildBody(tc.paragraphs, i)
				inputs <- Task{
					ID:     fmt.Sprintf("doc-%d", i),
					Reader: strings.NewReader(body),
				}
			}
			close(inputs)

			results := p.Run(ctx, tc.workers, inputs)

			var chunks, errs int
			for r := range results {
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

func TestPipeline_ErrorPropagation(t *testing.T) {
	p := &Pipeline{Parser: &parser.MarkdownParser{MaxBytes: 5}}
	ctx := context.Background()
	inputs := make(chan Task, 2)

	inputs <- Task{ID: "good", Reader: strings.NewReader("Hi")}
	inputs <- Task{ID: "bad", Reader: &failReader{}}
	close(inputs)

	var docs, errs int
	for r := range p.Run(ctx, 2, inputs) {
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

func TestPipeline_CancelDrainsWorkers(t *testing.T) {
	p := &Pipeline{Parser: &parser.MarkdownParser{}}
	ctx, cancel := context.WithCancel(context.Background())

	inputs := make(chan Task)
	results := p.Run(ctx, 4, inputs)

	cancel()
	close(inputs)

	done := make(chan struct{})
	go func() {
		for range results {
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline did not drain within 2s after cancel")
	}
}

func TestPipeline_RaceFree(t *testing.T) {
	const numJobs = 100
	p := &Pipeline{Parser: &parser.MarkdownParser{}}
	ctx := context.Background()
	inputs := make(chan Task, numJobs)

	for i := range numJobs {
		inputs <- Task{
			ID:     fmt.Sprintf("race-%d", i),
			Reader: strings.NewReader("A\n\nB"),
		}
	}
	close(inputs)

	var count atomic.Int64
	for r := range p.Run(ctx, 8, inputs) {
		if r.Err != nil {
			t.Fatalf("unexpected error: %v", r.Err)
		}
		count.Add(1)
	}

	if got := count.Load(); got != 200 {
		t.Fatalf("got %d chunks, want 200", got)
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
