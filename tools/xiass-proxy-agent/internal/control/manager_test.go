package control

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xyf0104/xiass-proxy-agent/internal/proxyroute"
)

func TestSnapshotSyncFixedAddressesPruneAndClear(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	preview, err := manager.Preview(context.Background(), []proxyroute.Source{{
		ID: "inline", Name: "Inline", Input: "http://127.0.0.1:18001#one\nhttp://127.0.0.1:18002#two",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Nodes) != 2 || preview.Snapshot == "" || len(manager.List()) != 0 {
		t.Fatalf("unexpected preview: nodes=%d snapshot=%t routes=%d", len(preview.Nodes), preview.Snapshot != "", len(manager.List()))
	}

	firstAddress := freeAddress(t)
	result, err := manager.Sync(context.Background(), preview.Snapshot,
		[]string{preview.Nodes[0].ID, preview.Nodes[0].ID},
		map[string]string{preview.Nodes[0].ID: firstAddress}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 || result.Routes[0].SOCKS5 != firstAddress {
		t.Fatalf("unexpected first sync: %+v", result.Routes)
	}
	assertListening(t, firstAddress)

	secondAddress := freeAddress(t)
	result, err = manager.Sync(context.Background(), preview.Snapshot,
		[]string{preview.Nodes[1].ID}, map[string]string{preview.Nodes[1].ID: secondAddress}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 2 {
		t.Fatalf("prune=false routes=%d", len(result.Routes))
	}
	assertListening(t, firstAddress)
	assertListening(t, secondAddress)

	result, err = manager.Sync(context.Background(), preview.Snapshot,
		[]string{preview.Nodes[1].ID}, map[string]string{preview.Nodes[1].ID: secondAddress}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 || result.Routes[0].SOCKS5 != secondAddress {
		t.Fatalf("unexpected prune result: %+v", result.Routes)
	}
	assertNotListening(t, firstAddress)
	assertListening(t, secondAddress)

	result, err = manager.Sync(context.Background(), "", []string{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 0 || len(result.Routes) != 0 || len(manager.List()) != 0 {
		t.Fatalf("clear did not empty routes: %+v", result)
	}
	assertNotListening(t, secondAddress)
}

func TestEmptyPreviewProducesRestorableEmptySnapshot(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	preview, err := manager.Preview(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Nodes) != 0 || preview.Snapshot == "" {
		t.Fatalf("unexpected empty preview: %+v", preview)
	}
	result, err := manager.Sync(context.Background(), preview.Snapshot, []string{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 0 || len(result.Routes) != 0 {
		t.Fatalf("empty snapshot restored non-empty state: %+v", result)
	}
}

func TestSyncFailureKeepsOldListenerIntact(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	preview, err := manager.Preview(context.Background(), []proxyroute.Source{{
		ID: "inline", Input: "http://127.0.0.1:18011#one\nhttp://127.0.0.1:18012#two",
	}})
	if err != nil {
		t.Fatal(err)
	}
	oldAddress := freeAddress(t)
	if _, err := manager.Sync(context.Background(), preview.Snapshot, []string{preview.Nodes[0].ID}, map[string]string{preview.Nodes[0].ID: oldAddress}, true); err != nil {
		t.Fatal(err)
	}

	blocked, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	_, err = manager.Sync(context.Background(), preview.Snapshot, []string{preview.Nodes[1].ID}, map[string]string{preview.Nodes[1].ID: blocked.Addr().String()}, true)
	if err == nil {
		t.Fatal("sync unexpectedly replaced routes despite listener failure")
	}
	routes := manager.List()
	if len(routes) != 1 || routes[0].ID != preview.Nodes[0].ID || routes[0].SOCKS5 != oldAddress {
		t.Fatalf("old routes changed after failed sync: %+v", routes)
	}
	assertListening(t, oldAddress)
}

func TestSnapshotIsPortableAcrossAgentRestarts(t *testing.T) {
	first := NewManager()
	defer first.Close()
	preview, err := first.Preview(context.Background(), []proxyroute.Source{{ID: "inline", Input: "http://127.0.0.1:18021"}})
	if err != nil {
		t.Fatal(err)
	}
	second := NewManager()
	defer second.Close()
	address := freeAddress(t)
	result, err := second.Sync(context.Background(), preview.Snapshot, []string{preview.Nodes[0].ID}, map[string]string{preview.Nodes[0].ID: address}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 || result.Routes[0].ID != preview.Nodes[0].ID || result.Routes[0].SOCKS5 != address {
		t.Fatalf("snapshot was not restored by a new manager: %+v", result.Routes)
	}
}

func TestConcurrentSyncsAreSerializedAndIdempotent(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	preview, err := manager.Preview(context.Background(), []proxyroute.Source{{ID: "inline", Input: "http://127.0.0.1:18031"}})
	if err != nil {
		t.Fatal(err)
	}
	address := freeAddress(t)
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := manager.Sync(context.Background(), preview.Snapshot, []string{preview.Nodes[0].ID}, map[string]string{preview.Nodes[0].ID: address}, true)
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	routes := manager.List()
	if len(routes) != 1 || routes[0].SOCKS5 != address {
		t.Fatalf("concurrent sync was not idempotent: %+v", routes)
	}
}

func TestListenAddressesRejectUnknownSnapshotNode(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	preview, err := manager.Preview(context.Background(), []proxyroute.Source{{ID: "inline", Input: "http://127.0.0.1:18041"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Sync(context.Background(), preview.Snapshot, []string{preview.Nodes[0].ID}, map[string]string{"route-unknown": freeAddress(t)}, true)
	if err == nil {
		t.Fatal("accepted listen address for node outside snapshot")
	}
	if len(manager.List()) != 0 {
		t.Fatal("failed strict validation changed active routes")
	}
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func assertListening(t *testing.T, address string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("expected listener at %s: %v", address, err)
	}
	_ = conn.Close()
}

func assertNotListening(t *testing.T, address string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("listener still active at %s", address)
	}
}
