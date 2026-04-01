// Package engine provides a bounded worker pool with deterministic shutdown.
package engine

import (
	"context"
	"fmt"
	"sync"
)

// Job is a unit of work submitted to a Dispatcher.
type Job struct {
	ID      string
	Execute func(ctx context.Context) error
}

// Dispatcher fans out Jobs to a fixed pool of goroutines. All workers
// are joinable after Close followed by Wait, whether shutdown was
// triggered by cancellation or normal channel drain.
type Dispatcher struct {
	jobs chan Job
	wg   sync.WaitGroup

	mu   sync.Mutex
	errs []error
}

// NewDispatcher returns a Dispatcher with the given job channel buffer.
func NewDispatcher(bufferSize int) *Dispatcher {
	return &Dispatcher{
		jobs: make(chan Job, bufferSize),
	}
}

// Start spawns numWorkers goroutines. Must be called exactly once.
func (d *Dispatcher) Start(ctx context.Context, numWorkers int) {
	for i := 0; i < numWorkers; i++ {
		d.wg.Add(1)
		go d.worker(ctx)
	}
}

// Submit enqueues a job. Blocks when the buffer is full. Returns an
// error if ctx expires before the job is accepted.
func (d *Dispatcher) Submit(ctx context.Context, j Job) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("dispatcher: submit %s: %w", j.ID, ctx.Err())
	case d.jobs <- j:
		return nil
	}
}

// Close signals that no more jobs will be submitted. Must precede Wait.
func (d *Dispatcher) Close() {
	close(d.jobs)
}

// Wait blocks until every worker goroutine has returned.
func (d *Dispatcher) Wait() {
	d.wg.Wait()
}

// Errs returns a snapshot of errors collected during execution.
func (d *Dispatcher) Errs() []error {
	d.mu.Lock()
	defer d.mu.Unlock()
	dst := make([]error, len(d.errs))
	copy(dst, d.errs)
	return dst
}

func (d *Dispatcher) worker(ctx context.Context) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-d.jobs:
			if !ok {
				return
			}
			// A buffered job may arrive after cancellation but before
			// the outer select re-evaluates ctx.Done.
			select {
			case <-ctx.Done():
				return
			default:
			}
			if err := j.Execute(ctx); err != nil {
				d.mu.Lock()
				d.errs = append(d.errs, fmt.Errorf("dispatcher: job %s: %w", j.ID, err))
				d.mu.Unlock()
			}
		}
	}
}
