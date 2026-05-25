package queue

import (
	"context"
	"time"
)

// MemoryQueue is a buffered channel queue — zero external dependencies.
// Suitable for single-process deployments. Jobs are lost on restart.
type MemoryQueue struct {
	ch chan Job
}

func NewMemoryQueue(size int) *MemoryQueue {
	if size <= 0 {
		size = 256
	}
	return &MemoryQueue{ch: make(chan Job, size)}
}

func (q *MemoryQueue) Enqueue(_ context.Context, job Job) error {
	if job.EnqueuedAt.IsZero() {
		job.EnqueuedAt = time.Now()
	}
	select {
	case q.ch <- job:
		return nil
	default:
		return context.DeadlineExceeded // queue full
	}
}

func (q *MemoryQueue) Dequeue(_ context.Context) (Job, bool, error) {
	select {
	case job := <-q.ch:
		return job, true, nil
	default:
		return Job{}, false, nil
	}
}

func (q *MemoryQueue) Complete(_ context.Context, _ string) error { return nil }
func (q *MemoryQueue) Fail(_ context.Context, _ string, _ error) error { return nil }
