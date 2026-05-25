package queue

import (
	"context"
	"time"
)

type Job struct {
	ID           string
	DeploymentID string
	RedeployFrom string            // previous deployment_id, empty for new
	Secrets      map[string]string // in-memory only, never persisted
	ConfigOnly   bool
	EnqueuedAt   time.Time
}

type Queue interface {
	Enqueue(ctx context.Context, job Job) error
	Dequeue(ctx context.Context) (Job, bool, error) // non-blocking; bool=false means empty
	Complete(ctx context.Context, jobID string) error
	Fail(ctx context.Context, jobID string, err error) error
}
