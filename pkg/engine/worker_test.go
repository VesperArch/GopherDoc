package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcher_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		workers   int
		jobs      int
		failEvery int
		wantErrs  int
		wantCount int64
	}{
		{"single_worker", 1, 1, 0, 0, 1},
		{"fan_out", 4, 20, 0, 0, 20},
		{"backpressure", 2, 50, 0, 0, 50},
		{"partial_failure", 4, 20, 5, 4, 16},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int64

			d := NewDispatcher(tc.jobs)
			ctx := context.Background()
			d.Start(ctx, tc.workers)

			for i := 1; i <= tc.jobs; i++ {
				id := fmt.Sprintf("job-%d", i)
				fail := tc.failEvery > 0 && i%tc.failEvery == 0
				if err := d.Submit(ctx, Job{
					ID: id,
					Execute: func(_ context.Context) error {
						if fail {
							return fmt.Errorf("deliberate")
						}
						count.Add(1)
						return nil
					},
				}); err != nil {
					t.Fatalf("submit %s: %v", id, err)
				}
			}

			d.Close()
			d.Wait()

			if got := count.Load(); got != tc.wantCount {
				t.Errorf("executed %d jobs, want %d", got, tc.wantCount)
			}
			if got := len(d.Errs()); got != tc.wantErrs {
				t.Errorf("got %d errors, want %d", got, tc.wantErrs)
			}
		})
	}
}

func TestDispatcher_ConcurrentRaceFree(t *testing.T) {
	const numJobs = 100

	var count atomic.Int64
	d := NewDispatcher(numJobs)
	ctx := context.Background()
	d.Start(ctx, 8)

	for i := range numJobs {
		id := fmt.Sprintf("race-%d", i)
		if err := d.Submit(ctx, Job{
			ID: id,
			Execute: func(_ context.Context) error {
				count.Add(1)
				return nil
			},
		}); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
	}

	d.Close()
	d.Wait()

	if got := count.Load(); got != numJobs {
		t.Fatalf("processed %d jobs, want %d", got, numJobs)
	}
}

func TestDispatcher_CancelStopsWorkers(t *testing.T) {
	d := NewDispatcher(0)
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx, 4)

	cancel()

	done := make(chan struct{})
	go func() {
		d.Close()
		d.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine leak: workers alive 2s after cancel")
	}
}

func TestDispatcher_SubmitAfterCancel(t *testing.T) {
	d := NewDispatcher(0)
	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx, 2)
	cancel()

	err := d.Submit(ctx, Job{
		ID:      "late",
		Execute: func(_ context.Context) error { return nil },
	})
	if err == nil {
		t.Fatal("expected error on submit after cancel")
	}

	d.Close()
	d.Wait()
}
