package control

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"reflect"
	"strings"
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

func TestDockerSubscriptionAllocatesAndRestoresTheSameLocalAddress(t *testing.T) {
	t.Setenv("XIASS_PROXY_AGENT_ROUTE_BIND_HOST", "0.0.0.0")
	first := NewManager()
	defer first.Close()
	preview, err := first.Preview(context.Background(), []proxyroute.Source{{ID: "inline", Input: "http://127.0.0.1:18021"}})
	if err != nil {
		t.Fatal(err)
	}
	selected := []string{preview.Nodes[0].ID}
	created, err := first.Sync(context.Background(), preview.Snapshot, selected, nil, true)
	if err != nil || len(created.Routes) != 1 {
		t.Fatalf("new subscription: routes=%v err=%v", created.Routes, err)
	}
	address := created.Routes[0].SOCKS5
	if !proxyroute.IsLoopbackAddress(address) {
		t.Fatalf("API cannot persist route address %q", address)
	}
	assertListening(t, address)
	first.Close()
	second := NewManager()
	defer second.Close()
	restored, err := second.Sync(context.Background(), preview.Snapshot, selected, map[string]string{selected[0]: address}, true)
	if err != nil || len(restored.Routes) != 1 || restored.Routes[0].SOCKS5 != address {
		t.Fatalf("restored subscription: routes=%v err=%v", restored.Routes, err)
	}
	assertListening(t, address)
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

func TestPreviewRetainingMissingNodesPreservesListenersAndRestart(t *testing.T) {
	ctx := context.Background()
	manager := NewManager()
	defer manager.Close()
	source := proxyroute.Source{ID: "inline", Name: "Inline", Input: "http://127.0.0.1:18443\nhttp://127.0.0.1:18001"}
	previous, err := manager.Preview(ctx, []proxyroute.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(previous.Nodes) != 2 {
		t.Fatalf("initial preview nodes=%d, want 2", len(previous.Nodes))
	}
	oldID := previous.Nodes[0].ID
	unchangedID := previous.Nodes[1].ID
	addresses := map[string]string{oldID: freeAddress(t), unchangedID: freeAddress(t)}
	if _, err := manager.Sync(ctx, previous.Snapshot, []string{oldID, unchangedID}, addresses, true); err != nil {
		t.Fatal(err)
	}
	oldRoute := manager.routes[oldID].route
	unchangedRoute := manager.routes[unchangedID].route

	source.Input = "http://127.0.0.1:18444\nhttp://127.0.0.1:18001"
	preview, err := manager.PreviewRetaining(ctx, []proxyroute.Source{source}, previous.Snapshot, []string{oldID, unchangedID, oldID})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Nodes) != 2 {
		t.Fatalf("fresh preview nodes=%d, want 2", len(preview.Nodes))
	}
	newID := preview.Nodes[0].ID
	if newID == oldID || len(preview.Nodes) != 2 || !reflect.DeepEqual(preview.RetainedNodeIDs, []string{oldID}) {
		t.Fatalf("unexpected retention: fresh=%v retained=%v", preview.Nodes, preview.RetainedNodeIDs)
	}
	if len(manager.List()) != 2 || manager.routes[oldID].route != oldRoute {
		t.Fatal("preview changed active listeners")
	}
	selected := []string{oldID, unchangedID, newID}
	for _, prune := range []bool{false, true} {
		result, err := manager.Sync(ctx, preview.Snapshot, selected, addresses, prune)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Routes) != 3 || manager.routes[oldID].route != oldRoute || manager.routes[unchangedID].route != unchangedRoute {
			t.Fatal("retention replaced existing listeners")
		}
		for _, route := range result.Routes {
			if address := addresses[route.ID]; address != "" && route.SOCKS5 != address {
				t.Fatal("retention changed a fixed address")
			}
			addresses[route.ID] = route.SOCKS5
			assertListening(t, route.SOCKS5)
		}
	}
	if addresses[newID] == addresses[oldID] {
		t.Fatal("renewed node reused the old node's listener")
	}

	manager.Close()
	restarted := NewManager()
	defer restarted.Close()
	result, err := restarted.Sync(ctx, preview.Snapshot, selected, addresses, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 3 {
		t.Fatalf("restored routes=%d", len(result.Routes))
	}
	for _, route := range result.Routes {
		if route.SOCKS5 != addresses[route.ID] {
			t.Fatal("restart changed a saved address")
		}
		assertListening(t, route.SOCKS5)
	}
}

func TestPreviewRetainingOnlyCarriesAllowlistedMissingNodes(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	ctx := context.Background()
	previous, err := manager.Preview(ctx, []proxyroute.Source{{ID: "old", Input: "http://127.0.0.1:18001#one\nhttp://127.0.0.1:18002#two"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(previous.Nodes) != 2 {
		t.Fatalf("initial preview nodes=%d, want 2", len(previous.Nodes))
	}
	oldID := previous.Nodes[0].ID
	for _, test := range []struct {
		name    string
		sources []proxyroute.Source
		retain  []string
	}{
		{name: "without allowlist", sources: []proxyroute.Source{{ID: "fresh", Input: "http://127.0.0.1:18003#one"}}},
		{name: "one exact id", sources: []proxyroute.Source{{ID: "fresh", Input: "http://127.0.0.1:18003#one"}}, retain: []string{oldID}},
		{name: "empty fresh sources", retain: []string{oldID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			preview, err := manager.PreviewRetaining(ctx, test.sources, previous.Snapshot, test.retain)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeAndBuildSnapshot(preview.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			defer proxyroute.CloseCandidates(decoded)
			if len(decoded) != len(preview.Nodes)+len(test.retain) || !reflect.DeepEqual(preview.RetainedNodeIDs, test.retain) {
				t.Fatalf("fresh=%d snapshot=%d retained=%v", len(preview.Nodes), len(decoded), preview.RetainedNodeIDs)
			}
			for _, candidate := range decoded {
				if candidate.SourceID == "old" && (len(test.retain) == 0 || candidate.ID != oldID) {
					t.Fatal("snapshot carried a non-allowlisted old node")
				}
			}
		})
	}
}

func TestPreviewRetainingFailsClosedWithoutChangingListeners(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	ctx := context.Background()
	sources := []proxyroute.Source{{ID: "inline", Input: "http://127.0.0.1:18001#old"}}
	previous, err := manager.Preview(ctx, sources)
	if err != nil {
		t.Fatal(err)
	}
	oldID := previous.Nodes[0].ID
	address := freeAddress(t)
	if _, err := manager.Sync(ctx, previous.Snapshot, []string{oldID}, map[string]string{oldID: address}, true); err != nil {
		t.Fatal(err)
	}
	oldRoute := manager.routes[oldID].route
	decoded, err := decodeAndBuildSnapshot(previous.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer proxyroute.CloseCandidates(decoded)
	duplicateSnapshot, err := encodeSnapshot(append(append([]proxyroute.Candidate(nil), decoded...), decoded[0]))
	if err != nil {
		t.Fatal(err)
	}
	decoded[0].ID = "invalid-canonical-id"
	invalidIdentity, err := encodeSnapshot(decoded)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(previous.Snapshot, snapshotPrefix))
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	invalidChecksum := snapshotPrefix + base64.RawURLEncoding.EncodeToString(data)
	for _, test := range []struct {
		name     string
		snapshot string
		retain   []string
	}{
		{name: "missing previous snapshot", retain: []string{oldID}},
		{name: "invalid snapshot", snapshot: "invalid", retain: []string{oldID}},
		{name: "invalid snapshot without allowlist", snapshot: "invalid"},
		{name: "invalid checksum", snapshot: invalidChecksum, retain: []string{oldID}},
		{name: "invalid canonical identity", snapshot: invalidIdentity, retain: []string{oldID}},
		{name: "duplicate snapshot ids", snapshot: duplicateSnapshot, retain: []string{oldID}},
		{name: "missing retained id", snapshot: previous.Snapshot, retain: []string{oldID, "unknown-id"}},
		{name: "too many retained ids", snapshot: previous.Snapshot, retain: make([]string, proxyroute.MaxNodes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := manager.PreviewRetaining(ctx, nil, test.snapshot, test.retain)
			if err == nil || result.Snapshot != "" || len(result.Nodes) != 0 || len(result.RetainedNodeIDs) != 0 {
				t.Fatal("invalid retention request returned a usable preview")
			}
			if len(manager.List()) != 1 || manager.routes[oldID].route != oldRoute {
				t.Fatal("failed preview changed active listeners")
			}
			assertListening(t, address)
		})
	}
}

func TestPreviewRetainingEnforcesMergedNodeLimit(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	ctx := context.Background()
	previous, err := manager.Preview(ctx, []proxyroute.Source{{ID: "old", Input: "http://127.0.0.1:18001"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{proxyroute.MaxNodes - 1, proxyroute.MaxNodes} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var input strings.Builder
			for index := range count {
				fmt.Fprintf(&input, "http://127.0.0.1:%d#node-%d\n", 20000+index, index)
			}
			preview, err := manager.PreviewRetaining(ctx, []proxyroute.Source{{ID: "fresh", Input: input.String()}}, previous.Snapshot, []string{previous.Nodes[0].ID})
			if count == proxyroute.MaxNodes {
				if err == nil || preview.Snapshot != "" {
					t.Fatal("accepted more than 256 merged nodes")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeAndBuildSnapshot(preview.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			defer proxyroute.CloseCandidates(decoded)
			if len(preview.Nodes) != count || len(decoded) != proxyroute.MaxNodes || !reflect.DeepEqual(preview.RetainedNodeIDs, []string{previous.Nodes[0].ID}) {
				t.Fatal("valid 256-node merged snapshot lost nodes")
			}
		})
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
