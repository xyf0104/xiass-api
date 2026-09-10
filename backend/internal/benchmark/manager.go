package benchmark

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager dispatches submitted probes on demand. The queue and account guards
// live in PostgreSQL; no customer scheduler or persistent worker pool is used.
type Manager struct {
	store       Store
	runner      Runner
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	owner       string
	mu          sync.Mutex
	started     bool
	stopped     bool
	dispatching bool
	pending     bool
	active      int
	retryMu     sync.Mutex
	retry       *time.Timer
}

const (
	finishRetryInitialDelay = time.Second
	finishRetryMaxDelay     = 30 * time.Second
	finishAttemptTimeout    = 5 * time.Second
)

func NewManager(store Store, runner Runner) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{store: store, runner: runner, ctx: ctx, cancel: cancel, owner: uuid.NewString()}
}

func (m *Manager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.stopped {
		return
	}
	m.started = true
	m.wakeLocked() // One startup drain recovers queued jobs, never running claims.
}

func (m *Manager) Wake() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wakeLocked()
}

func (m *Manager) wakeLocked() {
	if m.stopped {
		return
	}
	m.pending = true
	if m.started && !m.dispatching && m.active < MaxBatchSize {
		m.dispatching = true
		m.wg.Add(1)
		go m.dispatch()
	}
}

func (m *Manager) Stop() {
	// Serialize startup with shutdown so Wait never races an Add on an idle manager.
	m.mu.Lock()
	m.stopped = true
	m.cancel()
	m.mu.Unlock()
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
		if m.ctx.Err() != nil {
			return
		}
		m.Wake()
	})
}

func (m *Manager) dispatch() {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		if m.stopped || m.active >= MaxBatchSize {
			m.dispatching = false
			m.mu.Unlock()
			return
		}
		m.pending = false
		m.mu.Unlock()
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		task, err := m.store.Claim(ctx, m.owner)
		cancel()
		if err == nil && task != nil {
			m.mu.Lock()
			m.active++
			m.wg.Add(1)
			m.mu.Unlock()
			go func() {
				defer m.wg.Done()
				m.run(task)
				m.mu.Lock()
				m.active--
				m.wakeLocked()
				m.mu.Unlock()
			}()
			continue
		}
		m.mu.Lock()
		// A submission racing the final empty Claim must not lose its wakeup.
		if err == nil && m.pending && !m.stopped {
			m.mu.Unlock()
			continue
		}
		m.dispatching = false
		m.mu.Unlock()
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
				if err != nil || current == nil || current.Status != "running" {
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
		status, code = "interrupted", "runner_interrupted"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status, code = "failed", "timeout"
		}
	}
	if len(output.HTML) > MaxHTMLBytes {
		status, code, output.HTML = "failed", "html_too_large", ""
	}
	m.finish(task.ID, status, code, output.HTML)
}

// finish retries only the terminal database write. The upstream call has
// already returned, so this cannot create another request or release the
// account guard before the store acknowledges the transition.
func (m *Manager) finish(id, status, code, html string) {
	delay := finishRetryInitialDelay
	shutdownAttempt := false
	for {
		finishParent := m.ctx
		if shutdownAttempt {
			// Stop cancels normal work. Give one final bounded write a fresh
			// context so cancellation does not strand a completed upstream call.
			finishParent = context.Background()
		}
		finishCtx, finishCancel := context.WithTimeout(finishParent, finishAttemptTimeout)
		err := m.store.Finish(finishCtx, id, m.owner, status, code, html)
		finishCancel()
		if err == nil {
			return
		}
		if shutdownAttempt {
			slog.Error("pelican_benchmark_finalize_failed", "task_id", id)
			return
		}
		if m.ctx.Err() != nil {
			shutdownAttempt = true
			continue
		}

		slog.Warn("pelican_benchmark_finalize_retrying", "task_id", id)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-m.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			// The next iteration performs the single final bounded attempt.
		}
		if delay < finishRetryMaxDelay {
			delay *= 2
			if delay > finishRetryMaxDelay {
				delay = finishRetryMaxDelay
			}
		}
	}
}

func (m *Manager) invoke(ctx context.Context, task *Task) (out Output, err error) {
	defer func() {
		if recover() != nil {
			out = Output{ErrorCode: "runner_panic"}
			err = errors.New("benchmark runner panic")
		}
	}()
	if task.Action == "continue" {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		detail, err := m.store.Detail(readCtx, task.SourceID)
		cancel()
		if err != nil {
			return Output{ErrorCode: "continuation_source_unavailable"}, err
		}
		if detail == nil || task.SourceID == "" || detail.AccountID != task.AccountID || !ValidStatus(detail.Status) ||
			detail.Status == "queued" || detail.Status == "running" || detail.Status == "canceling" || len(detail.HTML) > MaxHTMLBytes {
			return Output{ErrorCode: "invalid_continuation_source"}, errors.New("invalid benchmark continuation source")
		}
		ctx = WithContinuation(ctx, detail.HTML)
	}
	return m.runner.RunPelicanBenchmark(ctx, task.AccountID, task.Model, func(model string) error {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return m.store.SetUpstreamModel(writeCtx, task.ID, m.owner, model)
	})
}
