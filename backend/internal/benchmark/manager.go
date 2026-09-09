package benchmark

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager follows the existing bounded admin probe worker pattern. The queue
// and claims live in PostgreSQL; no customer scheduler or usage worker is used.
type Manager struct {
	store   Store
	runner  Runner
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	start   sync.Once
	owner   string
	wake    chan struct{}
	retryMu sync.Mutex
	retry   *time.Timer
}

func NewManager(store Store, runner Runner) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{store: store, runner: runner, ctx: ctx, cancel: cancel, owner: uuid.NewString(), wake: make(chan struct{}, 1)}
}

func (m *Manager) Start() {
	m.start.Do(func() {
		m.wg.Add(1)
		go m.dispatch()
		m.Wake() // One startup drain recovers durable queued jobs, never running claims.
	})
}

func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Stop() {
	m.cancel()
	m.retryMu.Lock()
	if m.retry != nil {
		m.retry.Stop()
		m.retry = nil
	}
	m.retryMu.Unlock()
	m.wg.Wait()
}

func (m *Manager) retryLater(delay time.Duration) {
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	if m.ctx.Err() != nil {
		return
	}
	if m.retry != nil {
		return
	}
	m.retry = time.AfterFunc(delay, func() {
		m.retryMu.Lock()
		m.retry = nil
		m.retryMu.Unlock()
		m.Wake()
	})
}

func (m *Manager) dispatch() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
			var workers sync.WaitGroup
			for i := 0; i < 3; i++ {
				workers.Add(1)
				go func() { defer workers.Done(); m.worker() }()
			}
			workers.Wait()
		}
	}
}

func (m *Manager) worker() {
	for m.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		task, err := m.store.Claim(ctx, m.owner)
		cancel()
		if err == nil && task != nil {
			m.run(task)
			continue
		}
		if err != nil {
			// A transient storage error must not strand durable queued work, but
			// the retry is timer-driven so an idle manager remains dormant.
			m.retryLater(time.Second)
		}
		return
	}
}

func (m *Manager) run(task *Task) {
	ctx, cancel := context.WithTimeout(m.ctx, RunTimeout)
	defer cancel()
	done := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollCtx, pollCancel := context.WithTimeout(ctx, 2*time.Second)
				current, err := m.store.Get(pollCtx, task.ID)
				pollCancel()
				// Losing storage visibility must stop egress, not keep generating.
				if err != nil || current.Status != "running" {
					cancel()
					return
				}
			}
		}
	}()
	output, err := m.invoke(ctx, task)
	close(done)
	<-monitorDone
	status, code := "succeeded", output.ErrorCode
	if err != nil {
		status = "failed"
		if code == "" {
			code = "upstream_failed"
		}
	}
	if ctx.Err() != nil {
		status, code, output.HTML = "interrupted", "runner_interrupted", ""
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, code = "failed", "timeout"
		}
	}
	if len(output.HTML) > MaxHTMLBytes {
		status, code, output.HTML = "failed", "html_too_large", ""
	}
	if status != "succeeded" {
		output.HTML = ""
	}
	// Only the goroutine that has returned from the upstream call may release
	// the unique guard. Never release on cancellation request or lease expiry.
	for attempt := 0; attempt < 3; attempt++ {
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = m.store.Finish(finishCtx, task.ID, m.owner, status, code, output.HTML)
		finishCancel()
		if err == nil {
			return
		}
	}
	slog.Error("pelican_benchmark_finalize_failed", "task_id", task.ID)
}

func (m *Manager) invoke(ctx context.Context, task *Task) (out Output, err error) {
	defer func() {
		if recover() != nil {
			out = Output{ErrorCode: "runner_panic"}
			err = errors.New("benchmark runner panic")
		}
	}()
	return m.runner.RunPelicanBenchmark(ctx, task.AccountID, task.Model, func(model string) error {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return m.store.SetUpstreamModel(writeCtx, task.ID, m.owner, model)
	})
}
