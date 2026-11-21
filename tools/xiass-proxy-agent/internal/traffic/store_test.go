package traffic

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTrafficSurvivesRestartAndCalendarWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(zone)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	s.now = func() time.Time { return start.AddDate(0, 0, -30) }
	s.Register("jp", "Japan", "hysteria2")
	s.Add("jp", 1000, 2000)
	s.now = func() time.Time { return start.AddDate(0, 0, -29) }
	s.Add("jp", 10, 20)
	s.now = func() time.Time { return start.Add(-time.Second) }
	s.Add("jp", 30, 40)
	s.now = func() time.Time { return now }
	s.Add("jp", 50, 60)
	for _, tt := range []struct {
		days     int
		up, down int64
	}{{1, 50, 60}, {7, 80, 100}, {30, 90, 120}} {
		r := s.Report(tt.days)
		if len(r.Nodes) != 1 || r.Nodes[0].Upload != tt.up || r.Nodes[0].Download != tt.down || r.Nodes[0].Total != tt.up+tt.down {
			t.Fatalf("%d days: %+v", tt.days, r)
		}
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return now }
	restarted.Register("jp", "Renamed Japan", "hysteria2")
	r := restarted.Report(30)
	if r.Nodes[0].Total != 210 || r.Nodes[0].Name != "Renamed Japan" || !r.Nodes[0].StartedAt.Equal(start.AddDate(0, 0, -30)) || !r.PersistenceOK {
		t.Fatalf("lost history: %+v", r)
	}
}

func TestTrafficConcurrentCountersAndPersistence(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "traffic.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Register("jp", "Japan", "hysteria2")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				s.Add("jp", 13, 17)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if err := s.Flush(); err != nil {
				t.Error(err)
			}
			_ = s.Report(30)
		}
	}()
	wg.Wait()
	r := s.Report(30)
	if r.Nodes[0].Total != 8*1000*30 {
		t.Fatalf("lost or duplicated bytes: %+v", r)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(s.path, "invalid", "file")
	if err := s.Flush(); err == nil {
		t.Fatal("expected disk error")
	}
	s.Add("jp", 1, 2)
	if r := s.Report(30); r.PersistenceOK || r.Nodes[0].Total != 240003 {
		t.Fatalf("forwarding counters stopped on disk error: %+v", r)
	}
}

func TestCorruptTrafficHistoryIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected corrupt-history error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken" {
		t.Fatal("history overwritten")
	}
}
