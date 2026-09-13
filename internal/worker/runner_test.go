package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/smartx/sks-migration-center/internal/domain/migration"
)

type fakeJobStore struct {
	mu           sync.Mutex
	lease        *migration.Lease
	claimed      bool
	completed    bool
	retried      bool
	failed       bool
	heartbeatErr error
	claimErr     error
}

type fakeObserver struct {
	step    string
	outcome string
}

func (o *fakeObserver) ObserveJob(step, outcome string, _ time.Duration) {
	o.step, o.outcome = step, outcome
}

func (s *fakeJobStore) Claim(context.Context, string, time.Duration) (*migration.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		err := s.claimErr
		s.claimErr = nil
		return nil, err
	}
	if s.claimed || s.lease == nil {
		return nil, nil
	}
	s.claimed = true
	copy := *s.lease
	return &copy, nil
}

func TestRunnerRecoversAfterTransientDatabaseOutage(t *testing.T) {
	store := &fakeJobStore{lease: &migration.Lease{ID: uuid.New(), StepType: migration.StepPreflight}, claimErr: errors.New("database unavailable")}
	handled := make(chan struct{})
	runner, err := NewRunner(testConfig(), store, map[migration.StepType]Handler{
		migration.StepPreflight: HandlerFunc(func(context.Context, migration.Lease) error { close(handled); return nil }),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-handled:
		cancel()
	case <-ctx.Done():
		t.Fatal("worker did not recover after transient database failure")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (s *fakeJobStore) Heartbeat(context.Context, uuid.UUID, string, time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeatErr
}

func (s *fakeJobStore) Complete(context.Context, uuid.UUID, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = true
	return nil
}

func (s *fakeJobStore) Retry(context.Context, uuid.UUID, string, time.Time, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retried = true
	return nil
}

func (s *fakeJobStore) Fail(context.Context, uuid.UUID, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = true
	return nil
}

func testConfig() Config {
	return Config{
		Version:           "test",
		OwnerID:           "worker-test",
		PollInterval:      time.Millisecond,
		HeartbeatInterval: time.Millisecond,
		LeaseDuration:     10 * time.Millisecond,
		RetryDelay:        time.Millisecond,
		MaxAttempts:       5,
	}
}

func TestRunnerMarksJobFailedAfterRetryBudget(t *testing.T) {
	store := &fakeJobStore{lease: &migration.Lease{ID: uuid.New(), StepType: migration.StepRestore, Attempt: 5}}
	runner, err := NewRunner(testConfig(), store, map[migration.StepType]Handler{
		migration.StepRestore: HandlerFunc(func(context.Context, migration.Lease) error { return errors.New("permanent failure") }),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	processed, err := runner.processOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("processOne = %v, %v", processed, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.failed || store.retried || store.completed {
		t.Fatalf("unexpected exhausted state: failed=%v retried=%v completed=%v", store.failed, store.retried, store.completed)
	}
}

func TestRunnerCompletesSuccessfulJob(t *testing.T) {
	store := &fakeJobStore{lease: &migration.Lease{ID: uuid.New(), StepType: migration.StepPreflight}}
	handled := make(chan struct{})
	runner, err := NewRunner(testConfig(), store, map[migration.StepType]Handler{
		migration.StepPreflight: HandlerFunc(func(context.Context, migration.Lease) error {
			close(handled)
			return nil
		}),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-handled
	for i := 0; i < 20; i++ {
		store.mu.Lock()
		completed := store.completed
		store.mu.Unlock()
		if completed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.completed || store.retried {
		t.Fatalf("unexpected completion state: completed=%v retried=%v", store.completed, store.retried)
	}
}

func TestRunnerRetriesFailedJob(t *testing.T) {
	store := &fakeJobStore{lease: &migration.Lease{ID: uuid.New(), StepType: migration.StepRestore}}
	observer := &fakeObserver{}
	config := testConfig()
	config.Observer = observer
	runner, err := NewRunner(config, store, map[migration.StepType]Handler{
		migration.StepRestore: HandlerFunc(func(context.Context, migration.Lease) error {
			return errors.New("temporary cluster failure")
		}),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	processed, err := runner.processOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("processOne = %v, %v", processed, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.retried || store.completed {
		t.Fatalf("unexpected retry state: completed=%v retried=%v", store.completed, store.retried)
	}
	if observer.step != string(migration.StepRestore) || observer.outcome != "retry" {
		t.Fatalf("unexpected observation: %+v", observer)
	}
}

func TestNewRunnerRejectsUnsafeLeaseTiming(t *testing.T) {
	config := testConfig()
	config.HeartbeatInterval = config.LeaseDuration
	_, err := NewRunner(config, &fakeJobStore{}, nil, nil)
	if err == nil {
		t.Fatal("expected unsafe heartbeat timing to be rejected")
	}
}

func TestRunnerCancelsHandlerWhenLeaseIsLost(t *testing.T) {
	store := &fakeJobStore{
		lease:        &migration.Lease{ID: uuid.New(), StepType: migration.StepRestore},
		heartbeatErr: errors.New("lease owner changed"),
	}
	handlerCancelled := make(chan struct{})
	runner, err := NewRunner(testConfig(), store, map[migration.StepType]Handler{
		migration.StepRestore: HandlerFunc(func(ctx context.Context, _ migration.Lease) error {
			<-ctx.Done()
			close(handlerCancelled)
			return ctx.Err()
		}),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	processed, err := runner.processOne(context.Background())
	if !processed || err == nil {
		t.Fatalf("processOne = %v, %v; want processed lease-loss error", processed, err)
	}
	select {
	case <-handlerCancelled:
	default:
		t.Fatal("handler was not cancelled after lease loss")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.completed || store.retried {
		t.Fatalf("lost lease must not mutate job: completed=%v retried=%v", store.completed, store.retried)
	}
}
