package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/raziel-ai/raziel/internal/db"
	"github.com/raziel-ai/raziel/internal/provider"
	"github.com/raziel-ai/raziel/internal/queue"
	"github.com/raziel-ai/raziel/internal/storage"
)

type Worker struct {
	q         queue.Queue
	db        *db.DB
	store     storage.ArtifactStore
	providers *provider.Registry
	log       *zap.Logger
	concurrency int
}

func New(q queue.Queue, database *db.DB, store storage.ArtifactStore, providers *provider.Registry, log *zap.Logger, concurrency int) *Worker {
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Worker{
		q:           q,
		db:          database,
		store:       store,
		providers:   providers,
		log:         log,
		concurrency: concurrency,
	}
}

// Start begins processing jobs until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	sem := make(chan struct{}, w.concurrency)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, ok, err := w.q.Dequeue(ctx)
		if err != nil {
			w.log.Error("dequeue error", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}
		if !ok {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		sem <- struct{}{}
		go func(j queue.Job) {
			defer func() { <-sem }()
			djob := &DeployJob{
				job:       j,
				db:        w.db,
				store:     w.store,
				providers: w.providers,
				log:       w.log.With(zap.String("deployment_id", j.DeploymentID)),
			}
			djob.Run(ctx)
		}(job)
	}
}
