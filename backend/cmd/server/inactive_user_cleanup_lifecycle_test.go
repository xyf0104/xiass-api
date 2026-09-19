package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestInactiveUserCleanup_WireApplicationLifecycle(t *testing.T) {
	for _, filename := range []string{"wire.go", "wire_gen.go"} {
		t.Run(filename, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filename, nil, 0)
			require.NoError(t, err)
			functions := make(map[string]*ast.FuncDecl)
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					functions[fn.Name.Name] = fn
				}
			}
			render := func(node ast.Node) string {
				var buf bytes.Buffer
				require.NoError(t, format.Node(&buf, fset, node))
				return buf.String()
			}

			cleanup := functions["provideCleanup"]
			require.NotNil(t, cleanup)
			workerIndex := -1
			workerName := ""
			for i, param := range cleanup.Type.Params.List {
				if render(param.Type) == "*service.InactiveUserCleanupService" {
					workerIndex = i
					workerName = param.Names[0].Name
				}
			}
			require.NotEqual(t, -1, workerIndex, "application cleanup must root the inactive-user worker in Wire")
			stopCalls := 0
			ast.Inspect(cleanup.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok && render(call.Fun) == workerName+".Stop" {
					stopCalls++
				}
				return true
			})
			require.Equal(t, 1, stopCalls, "application cleanup must stop its inactive-user worker")
			if filename != "wire_gen.go" {
				return
			}

			initialize := functions["initializeApplication"]
			require.NotNil(t, initialize)
			calls := make(map[string]*ast.CallExpr)
			results := make(map[string]string)
			counts := make(map[string]int)
			for _, stmt := range initialize.Body.List {
				assign, ok := stmt.(*ast.AssignStmt)
				if !ok || len(assign.Rhs) != 1 {
					continue
				}
				if call, ok := assign.Rhs[0].(*ast.CallExpr); ok {
					name := render(call.Fun)
					counts[name]++
					calls[name] = call
					results[name] = render(assign.Lhs[0])
				}
			}
			const repositoryProvider = "repository.NewInactiveUserCleanupRepository"
			const serviceProvider = "service.ProvideInactiveUserCleanupService"
			require.Contains(t, calls, repositoryProvider)
			require.Contains(t, calls, serviceProvider)
			require.Contains(t, calls, "provideCleanup")
			require.Equal(t, 1, counts[repositoryProvider])
			require.Equal(t, 1, counts[serviceProvider], "construct the worker only once")
			args := calls[serviceProvider].Args
			require.Len(t, args, 6)
			require.Equal(t, results["service.NewAdminServiceWithConfig"], render(args[0]), "use the existing admin deletion service")
			require.Equal(t, results[repositoryProvider], render(args[1]))
			require.Equal(t, results["service.NewNotificationEmailService"], render(args[2]))
			require.Equal(t, results["repository.NewLeaderLockCache"], render(args[3]))
			require.Equal(t, results["repository.ProvideSQLDB"], render(args[4]))
			require.Equal(t, results["config.ProvideConfig"], render(args[5]))
			require.Equal(t, results[serviceProvider], render(calls["provideCleanup"].Args[workerIndex]))
			applicationCleanup := ""
			ast.Inspect(initialize.Body, func(node ast.Node) bool {
				if field, ok := node.(*ast.KeyValueExpr); ok && render(field.Key) == "Cleanup" {
					applicationCleanup = render(field.Value)
				}
				return true
			})
			require.Equal(t, results["provideCleanup"], applicationCleanup)
		})
	}
}

func TestInactiveUserCleanup_StartupAndApplicationCleanup(t *testing.T) {
	for _, tt := range []struct {
		name          string
		nilConfig     bool
		enabled       bool
		executionNode bool
		controlPlane  bool
		wantStarted   bool
	}{
		{name: "nil_config", nilConfig: true},
		{name: "disabled_by_default"},
		{name: "enabled_single_node", enabled: true, wantStarted: true},
		{name: "enabled_non_control_plane", enabled: true, executionNode: true},
		{name: "enabled_control_plane", enabled: true, executionNode: true, controlPlane: true, wantStarted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.InactiveUserCleanup.Enabled = tt.enabled
			cfg.Gateway.ExecutionNode.Enabled = tt.executionNode
			cfg.Gateway.ExecutionNode.ControlPlane = tt.controlPlane
			if tt.nilConfig {
				cfg = nil
			}
			lock := &inactiveCleanupLifecycleLock{attempted: make(chan struct{}, 1)}
			// The lock denies leadership, so no repository, email, or deletion call is allowed.
			deleter := &struct{ service.InactiveUserDeleter }{}
			repo := &struct {
				service.InactiveUserCleanupRepository
			}{}
			worker := service.ProvideInactiveUserCleanupService(deleter, repo, nil, lock, nil, cfg)
			t.Cleanup(worker.Stop)
			if tt.wantStarted {
				select {
				case <-lock.attempted:
				case <-time.After(time.Second):
					t.Fatal("enabled worker did not start")
				}
				require.True(t, inactiveCleanupWorkerRunning(t))
			} else {
				select {
				case <-lock.attempted:
					t.Fatal("disabled worker must not start")
				case <-time.After(50 * time.Millisecond):
				}
				require.False(t, inactiveCleanupWorkerRunning(t))
			}

			cleanup := newTestApplicationCleanup(worker)
			require.NotPanics(t, cleanup)
			// Check before the fallback Stop registered with t.Cleanup can hide a leak.
			require.Eventually(t, func() bool {
				return !inactiveCleanupWorkerRunning(t)
			}, time.Second, 5*time.Millisecond, "application cleanup must join the worker goroutine")
		})
	}
}

type inactiveCleanupLifecycleLock struct {
	service.LeaderLockCache
	attempted chan struct{}
}

func (l *inactiveCleanupLifecycleLock) TryAcquireLeaderLock(context.Context, string, string, time.Duration) (bool, error) {
	l.attempted <- struct{}{}
	return false, nil
}

func inactiveCleanupWorkerRunning(t *testing.T) bool {
	t.Helper()
	var profile bytes.Buffer
	require.NoError(t, pprof.Lookup("goroutine").WriteTo(&profile, 2))
	return bytes.Contains(profile.Bytes(), []byte("internal/service.(*InactiveUserCleanupService).Start."))
}
