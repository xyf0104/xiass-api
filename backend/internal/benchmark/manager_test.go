package benchmark

import (
	"context"
	"encoding/json"
	"errors"
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
		html = ""
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
	require.Eventually(t, func() bool { return store.claims.Load() == 3 }, time.Second, time.Millisecond)
	time.Sleep(1200 * time.Millisecond)
	require.EqualValues(t, 3, store.claims.Load(), "idle manager must not periodically query PostgreSQL")
	m.Wake()
	require.Eventually(t, func() bool { return store.claims.Load() == 6 }, time.Second, time.Millisecond)
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
		s.status, s.finishHTML = "canceled", ""
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
		require.Equal(t, 3, claims)

		time.Sleep(time.Second)
		synctest.Wait()
		_, _, claims, _ = store.snapshot()
		require.Equal(t, 6, claims)

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
