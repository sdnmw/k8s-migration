package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/migration"
)

var ErrNoHandler = errors.New("no migration step handler registered")

type JobStore interface {
	Claim(context.Context, string, time.Duration) (*migration.Lease, error)
	Heartbeat(context.Context, uuid.UUID, string, time.Duration) error
	Complete(context.Context, uuid.UUID, string) error
	Retry(context.Context, uuid.UUID, string, time.Time, string) error
	Fail(context.Context, uuid.UUID, string, string) error
}

type Handler interface {
	Handle(context.Context, migration.Lease) error
}

type HandlerFunc func(context.Context, migration.Lease) error

func (f HandlerFunc) Handle(ctx context.Context, lease migration.Lease) error {
	return f(ctx, lease)
}

type Config struct {
	Version           string
	OwnerID           string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	RetryDelay        time.Duration
	MaxAttempts       int
	Observer          JobObserver
}

type JobObserver interface {
	ObserveJob(step, outcome string, duration time.Duration)
}

type Runner struct {
	config   Config
	jobs     JobStore
	handlers map[migration.StepType]Handler
	logger   *slog.Logger
}

func NewRunner(config Config, jobs JobStore, handlers map[migration.StepType]Handler, logger *slog.Logger) (*Runner, error) {
	if config.OwnerID == "" || config.PollInterval <= 0 || config.HeartbeatInterval <= 0 || config.LeaseDuration <= 0 || config.RetryDelay <= 0 {
		return nil, errors.New("worker owner and positive timing configuration are required")
	}
	if config.HeartbeatInterval >= config.LeaseDuration {
		return nil, errors.New("worker heartbeat interval must be shorter than lease duration")
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 5
	}
	if jobs == nil {
		return nil, errors.New("worker job store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{config: config, jobs: jobs, handlers: handlers, logger: logger}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	r.logger.Info("worker started", "version", r.config.Version, "owner", r.config.OwnerID)
	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()

	for {
		processed, err := r.processOne(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Error("worker polling failed", "error", err)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			r.logger.Info("worker stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Runner) processOne(ctx context.Context) (bool, error) {
	lease, err := r.jobs.Claim(ctx, r.config.OwnerID, r.config.LeaseDuration)
	if err != nil {
		return false, fmt.Errorf("claim job: %w", err)
	}
	if lease == nil {
		return false, nil
	}
	started := time.Now()
	observe := func(outcome string) {
		if r.config.Observer != nil {
			r.config.Observer.ObserveJob(string(lease.StepType), outcome, time.Since(started))
		}
	}

	handler, ok := r.handlers[lease.StepType]
	if !ok {
		handler = HandlerFunc(func(context.Context, migration.Lease) error { return ErrNoHandler })
	}

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	heartbeatResult := make(chan error, 1)
	go r.heartbeat(jobCtx, cancel, *lease, heartbeatResult)

	handleErr := handler.Handle(jobCtx, *lease)
	cancel()
	if heartbeatErr := <-heartbeatResult; heartbeatErr != nil {
		observe("lease_lost")
		return true, fmt.Errorf("migration job lease lost: %w", heartbeatErr)
	}
	if handleErr != nil {
		if lease.Attempt >= r.config.MaxAttempts {
			r.logger.Error("migration step exhausted retries", "stepType", lease.StepType, "attempt", lease.Attempt, "error", handleErr)
			if err := r.jobs.Fail(context.WithoutCancel(ctx), lease.ID, r.config.OwnerID, handleErr.Error()); err != nil {
				observe("store_error")
				return true, fmt.Errorf("fail migration job: %w", err)
			}
			observe("failed")
			return true, nil
		}
		r.logger.Warn("migration step will retry", "stepType", lease.StepType, "attempt", lease.Attempt, "error", handleErr)
		if err := r.jobs.Retry(context.WithoutCancel(ctx), lease.ID, r.config.OwnerID, time.Now().Add(r.config.RetryDelay), handleErr.Error()); err != nil {
			observe("store_error")
			return true, fmt.Errorf("release failed job: %w", err)
		}
		observe("retry")
		return true, nil
	}
	if err := r.jobs.Complete(context.WithoutCancel(ctx), lease.ID, r.config.OwnerID); err != nil {
		observe("store_error")
		return true, fmt.Errorf("complete job: %w", err)
	}
	observe("success")
	return true, nil
}

func (r *Runner) heartbeat(ctx context.Context, cancel context.CancelFunc, lease migration.Lease, result chan<- error) {
	ticker := time.NewTicker(r.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			if err := r.jobs.Heartbeat(ctx, lease.ID, r.config.OwnerID, r.config.LeaseDuration); err != nil {
				if ctx.Err() != nil {
					result <- nil
					return
				}
				r.logger.Error("migration job heartbeat failed", "leaseId", lease.ID, "error", err)
				cancel()
				result <- err
				return
			}
		}
	}
}
