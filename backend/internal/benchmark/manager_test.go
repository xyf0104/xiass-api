package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type pelicanManagerStore struct {
	Store
	mu         sync.Mutex
	status     string
	finished   chan string
	finishHTML string
}

func (s *pelicanManagerStore) Get(context.Context, string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Task{Status: s.status}, nil
}
func (s *pelicanManagerStore) SetUpstreamModel(context.Context, string, string, string) error {
	return nil
}
func (s *pelicanManagerStore) Finish(_ context.Context, _, _, status, _, html string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == "canceling" {
		status = "canceled"
	}
	s.status = status
	s.finishHTML = html
	s.finished <- status
	return nil
}

type pelicanRunnerFunc func(context.Context, int64, string, func(string) error) (Output, error)

func (f pelicanRunnerFunc) RunPelicanBenchmark(c context.Context, id int64, m string, b func(string) error) (Output, error) {
	return f(c, id, m, b)
}

func TestPelicanCancelRetainsGuardUntilUpstreamActuallyReturns(t *testing.T) {
	store := &pelicanManagerStore{status: "running", finished: make(chan string, 1)}
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	runner := pelicanRunnerFunc(func(ctx context.Context, _ int64, _ string, _ func(string) error) (Output, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return Output{}, ctx.Err()
	})
	m := NewManager(store, runner)
	defer m.cancel()
	go m.run(&Task{ID: "task", AccountID: 1, Model: DefaultModel})
	<-started
	store.mu.Lock()
	store.status = "canceling"
	store.mu.Unlock()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream was not canceled")
	}
	select {
	case <-store.finished:
		t.Fatal("guard released while upstream still running")
	default:
	}
	store.mu.Lock()
	require.Equal(t, "canceling", store.status)
	store.mu.Unlock()
	close(release)
	select {
	case status := <-store.finished:
		require.Equal(t, "canceled", status)
	case <-time.After(time.Second):
		t.Fatal("cancel not finalized")
	}
}

func TestPelicanPanicAndOversizeNeverStoreHTML(t *testing.T) {
	for _, panicRun := range []bool{false, true} {
		store := &pelicanManagerStore{status: "running", finished: make(chan string, 1)}
		runner := pelicanRunnerFunc(func(context.Context, int64, string, func(string) error) (Output, error) {
			if panicRun {
				panic("synthetic")
			}
			return Output{HTML: string(make([]byte, MaxHTMLBytes+1))}, nil
		})
		m := NewManager(store, runner)
		m.run(&Task{ID: "task"})
		m.cancel()
		require.Equal(t, "failed", <-store.finished)
		require.Empty(t, store.finishHTML)
	}
}

func TestPelicanTaskMetadataCannotMarshalHTML(t *testing.T) {
	detail := Detail{Task: Task{ID: "test", Status: "succeeded", HTMLBytes: 13}, HTML: "<html></html>"}
	b, err := json.Marshal(detail.Task)
	require.NoError(t, err)
	var object map[string]any
	require.NoError(t, json.Unmarshal(b, &object))
	require.NotContains(t, object, "html")
	require.Nil(t, object["thumbnail_url"])
	require.Nil(t, object["duration_ms"])
	require.False(t, ValidModel("model\nheader"))
	require.True(t, ValidModel(DefaultModel))
	require.True(t, ValidStatus("canceling"))
}

type pelicanIdleStore struct {
	Store
	claims atomic.Int64
}

func (s *pelicanIdleStore) Claim(context.Context, string) (*Task, error) {
	s.claims.Add(1)
	return nil, nil
}

func TestPelicanIdleManagerDoesNotPollDatabase(t *testing.T) {
	store := &pelicanIdleStore{}
	m := NewManager(store, nil)
	m.Start()
	defer m.Stop()
	require.Eventually(t, func() bool { return store.claims.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(1200 * time.Millisecond)
	require.EqualValues(t, 1, store.claims.Load(), "idle manager must not periodically query PostgreSQL")
	m.mu.Lock()
	idle := !m.dispatching && m.active == 0
	m.mu.Unlock()
	require.True(t, idle, "idle manager must not retain a dispatcher or workers")
	m.Wake()
	require.Eventually(t, func() bool { return store.claims.Load() == 2 }, time.Second, time.Millisecond)
}

type pelicanTransientStore struct {
	Store
	mu             sync.Mutex
	status         string
	task           Task
	claimErr       error
	claimed        bool
	claims         int
	finishErr      error
	finishAttempts int
	finishHTML     string
}

func (s *pelicanTransientStore) Claim(context.Context, string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if s.claimed {
		return nil, nil
	}
	s.claimed = true
	s.status = "running"
	task := s.task
	return &task, nil
}

func (s *pelicanTransientStore) Get(context.Context, string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.task
	task.Status = s.status
	return &task, nil
}

func (s *pelicanTransientStore) Finish(_ context.Context, _, _, status, _, html string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishAttempts++
	if s.finishErr != nil {
		return s.finishErr
	}
	if s.status == "canceling" {
		s.status, s.finishHTML = "canceled", html
		return nil
	}
	s.status, s.finishHTML = status, html
	return nil
}

func (s *pelicanTransientStore) snapshot() (status, html string, claims, finishAttempts int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.finishHTML, s.claims, s.finishAttempts
}

func TestPelicanClaimErrorsRecoverQueuedWorkWithoutWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanTransientStore{
			status:   "queued",
			task:     Task{ID: "queued-task", AccountID: 1, Model: DefaultModel},
			claimErr: errors.New("database unavailable"),
		}
		var runs atomic.Int64
		m := NewManager(store, pelicanRunnerFunc(func(context.Context, int64, string, func(string) error) (Output, error) {
			runs.Add(1)
			return Output{HTML: "<html>recovered</html>"}, nil
		}))
		m.Start()
		defer m.Stop()
		synctest.Wait()
		_, _, claims, _ := store.snapshot()
		require.Equal(t, 1, claims)

		time.Sleep(time.Second)
		synctest.Wait()
		_, _, claims, _ = store.snapshot()
		require.Equal(t, 2, claims)

		// The same durable queued task becomes claimable without a new Wake or Create.
		store.mu.Lock()
		store.claimErr = nil
		store.mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()
		status, html, _, _ := store.snapshot()
		require.EqualValues(t, 1, runs.Load())
		require.Equal(t, "succeeded", status)
		require.Equal(t, "<html>recovered</html>", html)
	})
}

func TestPelicanFinishErrorRetriesWithoutRerunningUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanTransientStore{
			status:    "queued",
			task:      Task{ID: "finish-task", AccountID: 1, Model: DefaultModel},
			finishErr: errors.New("database unavailable"),
		}
		var runs atomic.Int64
		m := NewManager(store, pelicanRunnerFunc(func(context.Context, int64, string, func(string) error) (Output, error) {
			runs.Add(1)
			return Output{HTML: "<html>result</html>"}, nil
		}))
		m.Start()
		defer m.Stop()
		synctest.Wait()
		_, _, _, attempts := store.snapshot()
		require.Equal(t, 1, attempts)

		time.Sleep(time.Second)
		synctest.Wait()
		_, _, _, attempts = store.snapshot()
		require.Equal(t, 2, attempts)

		// Protect the fault-injection state with the same mutex as Finish.
		store.mu.Lock()
		store.finishErr = nil
		store.mu.Unlock()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		status, html, _, attempts := store.snapshot()
		require.EqualValues(t, 1, runs.Load(), "Finish retry must never replay upstream")
		require.Equal(t, 3, attempts)
		require.Equal(t, "succeeded", status)
		require.Equal(t, "<html>result</html>", html)
	})
}

// This store models the durable claim/owner boundary shared by multiple managers.
type pelicanDispatchStore struct {
	Store
	mu       sync.Mutex
	tasks    []Task
	owners   map[string]string
	claims   int
	finishes int
}

func (s *pelicanDispatchStore) add(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range n {
		id := len(s.tasks) + 1
		s.tasks = append(s.tasks, Task{ID: fmt.Sprint(id), AccountID: int64(id), Status: "queued", Model: DefaultModel})
	}
}

func (s *pelicanDispatchStore) Claim(_ context.Context, owner string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	for i := range s.tasks {
		if s.tasks[i].Status != "queued" {
			continue
		}
		s.tasks[i].Status = "running"
		if s.owners == nil {
			s.owners = make(map[string]string)
		}
		s.owners[s.tasks[i].ID] = owner
		task := s.tasks[i]
		return &task, nil
	}
	return nil, nil
}

func (s *pelicanDispatchStore) Get(_ context.Context, id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.tasks {
		if task.ID == id {
			return &task, nil
		}
	}
	return nil, ErrNotFound
}

func (s *pelicanDispatchStore) Finish(_ context.Context, id, owner, status, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owners[id] != owner {
		return ErrStopped
	}
	for i := range s.tasks {
		if s.tasks[i].ID == id {
			if s.tasks[i].Status == "canceling" {
				status = "canceled"
			}
			s.tasks[i].Status = status
			s.finishes++
			delete(s.owners, id)
			return nil
		}
	}
	return ErrNotFound
}

func TestPelicanDispatchStartsAllAccountsAndNewSubmissionsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanDispatchStore{}
		store.add(8)
		var entered atomic.Int64
		m := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, _ int64, _ string, _ func(string) error) (Output, error) {
			entered.Add(1)
			<-ctx.Done()
			return Output{}, ctx.Err()
		}))
		m.Start()
		defer m.Stop()
		synctest.Wait()
		require.EqualValues(t, 8, entered.Load(), "all eight must enter before any runner is released")
		store.add(1)
		m.Wake()
		synctest.Wait()
		require.EqualValues(t, 9, entered.Load(), "new submissions must not wait for existing runners")
		m.Stop()
		require.Equal(t, 9, store.finishes)
		require.Empty(t, store.owners)
		claims := store.claims
		m.Wake()
		m.Start()
		synctest.Wait()
		require.Equal(t, claims, store.claims, "shutdown cannot restart dispatch")
	})
}

func TestPelicanDispatchRespectsMaxBatchSizeAndRefillsFreedSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanDispatchStore{}
		store.add(MaxBatchSize + 1)
		var entered atomic.Int64
		release := make(chan struct{})
		m := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, id int64, _ string, _ func(string) error) (Output, error) {
			entered.Add(1)
			if id == 1 {
				select {
				case <-release:
				case <-ctx.Done():
				}
			} else {
				<-ctx.Done()
			}
			return Output{}, ctx.Err()
		}))
		m.Start()
		defer m.Stop()
		synctest.Wait()
		require.EqualValues(t, MaxBatchSize, entered.Load())
		m.Wake()
		synctest.Wait()
		require.EqualValues(t, MaxBatchSize, entered.Load())
		close(release)
		synctest.Wait()
		require.EqualValues(t, MaxBatchSize+1, entered.Load())
		m.Stop()
		require.Equal(t, MaxBatchSize+1, store.finishes)
	})
}

func TestPelicanManagersShareStoreClaimsAndRetainCancelGuard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanDispatchStore{}
		store.add(8)
		var entered atomic.Int64
		release := make(chan struct{})
		runner := pelicanRunnerFunc(func(ctx context.Context, _ int64, _ string, _ func(string) error) (Output, error) {
			entered.Add(1)
			<-ctx.Done()
			<-release
			return Output{}, ctx.Err()
		})
		a, b := NewManager(store, runner), NewManager(store, runner)
		defer a.Stop()
		defer b.Stop()
		defer close(release)
		a.Start()
		b.Start()
		synctest.Wait()
		require.EqualValues(t, 8, entered.Load(), "store claims must not be bypassed across managers")
		store.mu.Lock()
		store.tasks[0].Status = "canceling"
		store.mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()
		a.Wake()
		b.Wake()
		synctest.Wait()
		store.mu.Lock()
		require.Equal(t, "canceling", store.tasks[0].Status)
		require.Len(t, store.owners, 8, "no guard can be released before the runner returns")
		require.Zero(t, store.finishes)
		store.mu.Unlock()
		require.EqualValues(t, 8, entered.Load())
	})
}

func TestPelicanFailureAndCancellationRetainPartialHTML(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			store := &pelicanManagerStore{status: "running", finished: make(chan string, 1)}
			m := NewManager(store, nil)
			defer m.Stop()
			m.runner = pelicanRunnerFunc(func(context.Context, int64, string, func(string) error) (Output, error) {
				if canceled {
					store.mu.Lock()
					store.status = "canceling"
					store.mu.Unlock()
					m.cancel()
				}
				return Output{HTML: "<html>partial model text"}, errors.New("upstream disconnected")
			})
			m.run(&Task{ID: "partial"})
			status := <-store.finished
			if canceled {
				require.Equal(t, "canceled", status)
			} else {
				require.Equal(t, "failed", status)
			}
			require.Equal(t, "<html>partial model text", store.finishHTML)
		})
	}
}

type pelicanContinuationStore struct {
	Store
	detail    *Detail
	err       error
	requested string
}

func (s *pelicanContinuationStore) Detail(_ context.Context, id string) (*Detail, error) {
	s.requested = id
	return s.detail, s.err
}

func TestPelicanContinuationValidatesSourceBeforeRunner(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		account int64
		err     error
		valid   bool
	}{
		{name: "succeeded", status: "succeeded", account: 7, valid: true},
		{name: "failed partial", status: "failed", account: 7, valid: true},
		{name: "canceled partial", status: "canceled", account: 7, valid: true},
		{name: "interrupted partial", status: "interrupted", account: 7, valid: true},
		{name: "wrong account", status: "succeeded", account: 8},
		{name: "queued", status: "queued", account: 7},
		{name: "running", status: "running", account: 7},
		{name: "canceling", status: "canceling", account: 7},
		{name: "unknown status", account: 7},
		{name: "missing", err: ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &pelicanContinuationStore{detail: &Detail{Task: Task{AccountID: tc.account, Status: tc.status}, HTML: "<html>previous partial"}, err: tc.err}
			called := false
			m := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, id int64, model string, _ func(string) error) (Output, error) {
				called = true
				text, ok := Continuation(ctx)
				require.True(t, ok)
				require.Equal(t, store.detail.HTML, text)
				require.EqualValues(t, 7, id)
				require.Equal(t, DefaultModel, model)
				return Output{HTML: text + "</html>"}, nil
			}))
			defer m.Stop()
			_, err := m.invoke(context.Background(), &Task{ID: "next", SourceID: "source", Action: "continue", AccountID: 7, Model: DefaultModel})
			require.Equal(t, "source", store.requested)
			require.Equal(t, tc.valid, called)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPelicanStopBeforeStartAndConcurrentWake(t *testing.T) {
	store := &pelicanIdleStore{}
	m := NewManager(store, nil)
	m.Stop()
	m.Start()
	m.Wake()
	require.Zero(t, store.claims.Load())
	for range 100 {
		m := NewManager(&pelicanIdleStore{}, nil)
		var calls sync.WaitGroup
		for _, f := range []func(){m.Start, m.Wake, m.Stop} {
			calls.Add(1)
			go func() { defer calls.Done(); f() }()
		}
		calls.Wait()
		m.Stop()
	}
}

func TestPelicanStopMakesFinalBoundedFinishAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanTransientStore{task: Task{ID: "finish-stop"}, finishErr: errors.New("storage down")}
		m := NewManager(store, pelicanRunnerFunc(func(context.Context, int64, string, func(string) error) (Output, error) {
			return Output{HTML: "partial"}, nil
		}))
		m.Start()
		synctest.Wait()
		_, _, _, before := store.snapshot()
		require.Equal(t, 1, before)
		m.Stop()
		_, _, _, after := store.snapshot()
		require.Equal(t, before+2, after, "one canceled normal write and one final shutdown write")
		m.mu.Lock()
		require.Zero(t, m.active)
		require.False(t, m.dispatching)
		m.mu.Unlock()
	})
}

type pelicanRacingClaimStore struct {
	*pelicanDispatchStore
	observed chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (s *pelicanRacingClaimStore) Claim(ctx context.Context, owner string) (*Task, error) {
	task, err := s.pelicanDispatchStore.Claim(ctx, owner)
	s.once.Do(func() {
		close(s.observed)
		<-s.release
	})
	return task, err
}

func TestPelicanWakeRacingEmptyClaimIsNotLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanRacingClaimStore{pelicanDispatchStore: &pelicanDispatchStore{}, observed: make(chan struct{}), release: make(chan struct{})}
		var entered atomic.Int64
		m := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, _ int64, _ string, _ func(string) error) (Output, error) {
			entered.Add(1)
			<-ctx.Done()
			return Output{}, ctx.Err()
		}))
		m.Start()
		defer m.Stop()
		<-store.observed
		store.add(1)
		m.Wake()
		close(store.release)
		synctest.Wait()
		require.EqualValues(t, 1, entered.Load())
	})
}

func TestPelicanStopWaitsForClaimAlreadyInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanRacingClaimStore{pelicanDispatchStore: &pelicanDispatchStore{}, observed: make(chan struct{}), release: make(chan struct{})}
		store.add(1)
		m := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, _ int64, _ string, _ func(string) error) (Output, error) {
			return Output{HTML: "partial"}, ctx.Err()
		}))
		m.Start()
		<-store.observed
		stopped := make(chan struct{})
		go func() { m.Stop(); close(stopped) }()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("Stop returned before the in-flight claim was finalized")
		default:
		}
		close(store.release)
		<-stopped
		require.Equal(t, 1, store.finishes)
		require.Empty(t, store.owners)
		require.Equal(t, "interrupted", store.tasks[0].Status)
	})
}

func TestPelicanStopCancelsPendingClaimRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanTransientStore{claimErr: errors.New("storage down")}
		m := NewManager(store, nil)
		m.Start()
		synctest.Wait()
		m.Stop()
		_, _, before, _ := store.snapshot()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		_, _, after, _ := store.snapshot()
		require.Equal(t, before, after)
	})
}

func TestPelicanContinuationRejectsNilAndOversizeSource(t *testing.T) {
	for _, detail := range []*Detail{nil, {Task: Task{AccountID: 7, Status: "failed"}, HTML: string(make([]byte, MaxHTMLBytes+1))}} {
		store := &pelicanContinuationStore{detail: detail}
		m := NewManager(store, pelicanRunnerFunc(func(context.Context, int64, string, func(string) error) (Output, error) {
			t.Error("invalid continuation must not invoke runner")
			return Output{}, nil
		}))
		out, err := m.invoke(context.Background(), &Task{Action: "continue", SourceID: "source", AccountID: 7})
		m.Stop()
		require.Error(t, err)
		require.Equal(t, "invalid_continuation_source", out.ErrorCode)
	}
}

func TestPelicanNormalRunDoesNotLoadContinuation(t *testing.T) {
	// No Detail implementation: a normal run must never query source HTML.
	store := &pelicanIdleStore{}
	called := false
	m := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, _ int64, _ string, _ func(string) error) (Output, error) {
		called = true
		_, ok := Continuation(ctx)
		require.False(t, ok)
		return Output{}, nil
	}))
	defer m.Stop()
	_, err := m.invoke(context.Background(), &Task{AccountID: 7})
	require.NoError(t, err)
	require.True(t, called)
}
