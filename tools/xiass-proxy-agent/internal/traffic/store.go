// Package traffic records tunnel payload bytes, independently of account tokens.
package traffic

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var zone = time.FixedZone("Asia/Shanghai", 8*60*60)

type Bytes struct {
	Upload   int64 `json:"upload_bytes"`
	Download int64 `json:"download_bytes"`
}

type Node struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Protocol  string           `json:"protocol"`
	StartedAt time.Time        `json:"started_at"`
	Days      map[string]Bytes `json:"days"`
}

type diskState struct {
	Version   int              `json:"version"`
	StartedAt time.Time        `json:"started_at"`
	Nodes     map[string]*Node `json:"nodes"`
}

type Row struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"`
	StartedAt time.Time `json:"started_at"`
	Bytes
	Total int64 `json:"total_bytes"`
}

type Report struct {
	StartedAt     time.Time  `json:"started_at"`
	PeriodStart   time.Time  `json:"period_start"`
	PeriodEnd     time.Time  `json:"period_end"`
	LastSavedAt   *time.Time `json:"last_saved_at"`
	PersistenceOK bool       `json:"persistence_ok"`
	Days          int        `json:"days"`
	Nodes         []Row      `json:"nodes"`
}

type Store struct {
	mu            sync.Mutex
	flushMu       sync.Mutex
	state         diskState
	path          string
	now           func() time.Time
	lastSaved     *time.Time
	persistenceOK bool
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now}
	s.state = diskState{Version: 1, StartedAt: s.now(), Nodes: map[string]*Node{}}
	data, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(data, &s.state) != nil || s.state.Version != 1 || s.state.Nodes == nil || s.state.StartedAt.IsZero() {
			return nil, errors.New("invalid traffic history; existing file preserved")
		}
		for id, n := range s.state.Nodes {
			if n == nil || n.ID != id || n.StartedAt.IsZero() || n.Days == nil {
				return nil, errors.New("invalid traffic node history")
			}
			for date, b := range n.Days {
				if _, err := time.Parse("2006-01-02", date); err != nil || b.Upload < 0 || b.Download < 0 {
					return nil, errors.New("invalid traffic bucket")
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := s.Flush(); err != nil {
		return nil, err
	}
	return s, nil
}

// Register is called before accepting connections. Stable node IDs survive
// subscription refreshes and socket port changes.
func (s *Store) Register(id, name, protocol string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.state.Nodes[id]
	if n == nil {
		n = &Node{ID: id, StartedAt: s.now(), Days: map[string]Bytes{}}
		s.state.Nodes[id] = n
	}
	n.Name, n.Protocol = name, protocol
}

func (s *Store) Add(id string, upload, download int64) {
	if s == nil || upload < 0 || download < 0 || upload+download == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.state.Nodes[id]
	if n == nil {
		return
	}
	day := s.now().In(zone).Format("2006-01-02")
	b := n.Days[day]
	b.Upload += upload
	b.Download += download
	n.Days[day] = b
}

func (s *Store) Report(days int) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(zone)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, 1-days)
	first, last := start.Format("2006-01-02"), now.Format("2006-01-02")
	r := Report{StartedAt: s.state.StartedAt, PeriodStart: start, PeriodEnd: now, Days: days, LastSavedAt: s.lastSaved, PersistenceOK: s.persistenceOK, Nodes: []Row{}}
	for _, n := range s.state.Nodes {
		row := Row{ID: n.ID, Name: n.Name, Protocol: n.Protocol, StartedAt: n.StartedAt}
		for day, b := range n.Days {
			if day >= first && day <= last {
				row.Upload += b.Upload
				row.Download += b.Download
			}
		}
		row.Total = row.Upload + row.Download
		r.Nodes = append(r.Nodes, row)
	}
	sort.Slice(r.Nodes, func(i, j int) bool { return r.Nodes[i].ID < r.Nodes[j].ID })
	return r
}

// Flush never runs in the forwarding path. Atomic replacement preserves the
// previous complete snapshot on write errors; only the last five seconds can
// be lost on a hard power failure.
func (s *Store) Flush() (err error) {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.persistenceOK = err == nil
		if err == nil {
			saved := s.now()
			s.lastSaved = &saved
		}
	}()
	s.mu.Lock()
	cutoff := s.now().In(zone).AddDate(0, 0, -35).Format("2006-01-02")
	for _, n := range s.state.Nodes {
		for day := range n.Days {
			if day < cutoff {
				delete(n.Days, day)
			}
		}
	}
	data, err := json.Marshal(s.state)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".traffic-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	return nil
}
