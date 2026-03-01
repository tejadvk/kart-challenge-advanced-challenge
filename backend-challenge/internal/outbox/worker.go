package outbox

import (
	"context"
	"log"
	"time"
)

// WorkerConfig configures the outbox worker
type WorkerConfig struct {
	PollInterval        time.Duration // default 2s
	BatchSize           int           // default 10
	StaleResetMinutes   int           // reset processing events older than this; 0 = disable
	FailedRetryMinutes  int           // reset failed events for retry after this; 0 = disable
	FailedRetryBatch    int           // max failed events to reset per cycle
}

// Worker polls the outbox and publishes events via the configured Publisher
type Worker struct {
	repo      *Repository
	publisher Publisher
	cfg       WorkerConfig
}

// NewWorker creates a new outbox worker
func NewWorker(repo *Repository, publisher Publisher, cfg WorkerConfig) *Worker {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	return &Worker{
		repo:      repo,
		publisher: publisher,
		cfg:       cfg,
	}
}

// Run starts the worker. It blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	// Reset stale processing on startup
	if w.cfg.StaleResetMinutes > 0 {
		n, err := w.repo.ResetStaleProcessing(ctx, w.cfg.StaleResetMinutes)
		if err != nil {
			log.Printf("[outbox] reset stale processing failed: %v", err)
		} else if n > 0 {
			log.Printf("[outbox] reset %d stale processing event(s)", n)
		}
	}

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	// Run once immediately
	w.processBatch(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Reset failed events for retry (transient broker failures)
			if w.cfg.FailedRetryMinutes > 0 && w.cfg.FailedRetryBatch > 0 {
				if n, err := w.repo.ResetFailedForRetry(ctx, w.cfg.FailedRetryMinutes, w.cfg.FailedRetryBatch); err != nil {
					log.Printf("[outbox] reset failed for retry failed: %v", err)
				} else if n > 0 {
					log.Printf("[outbox] reset %d failed event(s) for retry", n)
				}
			}
			w.processBatch(ctx)
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) {
	events, err := w.repo.ClaimPending(ctx, w.cfg.BatchSize)
	if err != nil {
		log.Printf("[outbox] claim pending failed: %v", err)
		return
	}
	if len(events) == 0 {
		return
	}

	for _, e := range events {
		ev := &EventToPublish{
			ID:            e.ID,
			AggregateType: e.AggregateType,
			AggregateID:   e.AggregateID,
			EventType:     e.EventType,
			Payload:       e.Payload,
		}
		if err := w.publisher.Publish(ctx, ev); err != nil {
			if markErr := w.repo.MarkFailed(ctx, e.ID, err.Error()); markErr != nil {
				log.Printf("[outbox] mark failed error: %v", markErr)
			}
			continue
		}
		if err := w.repo.MarkProcessed(ctx, e.ID); err != nil {
			log.Printf("[outbox] mark processed error: %v", err)
		}
	}
}
