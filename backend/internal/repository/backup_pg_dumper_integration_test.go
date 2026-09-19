//go:build integration

package repository

import (
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPgDumperUsesEffectiveRemoteDatabaseWithoutLocalFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := func(marker string) *tcpostgres.PostgresContainer {
		t.Helper()
		ctr, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23",
			tcpostgres.WithDatabase("snapshot"), tcpostgres.WithUsername("snapshot"),
			tcpostgres.WithPassword("synthetic-snapshot-password"), tcpostgres.BasicWaitStrategies(),
			testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
				hc.PortBindings = nat.PortMap{"5432/tcp": {{HostIP: "127.0.0.1"}}}
			}))
		require.NoError(t, err)
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			require.NoError(t, ctr.Terminate(cleanupCtx))
		})
		code, _, err := ctr.Exec(ctx, []string{"psql", "-U", "snapshot", "-d", "snapshot", "-v", "ON_ERROR_STOP=1", "-c",
			"CREATE TABLE snapshot_identity (name text); INSERT INTO snapshot_identity VALUES ('" + marker + "');"})
		require.NoError(t, err)
		require.Zero(t, code)
		return ctr
	}
	local, remote := start("LOCAL_OLD"), start("REMOTE_CURRENT")
	ip, err := remote.ContainerIP(ctx)
	require.NoError(t, err)
	remoteDSN, err := remote.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	docker, err := exec.LookPath("docker")
	require.NoError(t, err)
	bin := t.TempDir()
	// Run the real pg_dump in the existing isolated Docker bridge, where the
	// container addresses are reachable. This shim never chooses the source.
	shim := "#!/bin/sh\nexec '" + strings.ReplaceAll(docker, "'", "'\\''") + "' run --rm --network bridge -e PGPASSWORD -e PGSSLMODE postgres:18.1-alpine3.23 pg_dump \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "pg_dump"), []byte(shim), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	db, err := sql.Open("postgres", remoteDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))
	dumper := NewPgDumper(&config.Config{Database: config.DatabaseConfig{
		Host: ip, Port: 5432, User: "snapshot", DBName: "snapshot", Password: "synthetic-snapshot-password", SSLMode: "disable",
	}}, db)
	check := func() {
		t.Helper()
		stream, err := dumper.Dump(ctx)
		require.NoError(t, err)
		data, err := io.ReadAll(stream)
		require.NoError(t, err)
		require.NoError(t, stream.Close())
		require.Contains(t, string(data), "REMOTE_CURRENT")
		require.NotContains(t, string(data), "LOCAL_OLD")
	}
	check()
	stopTimeout := time.Second
	require.NoError(t, local.Stop(ctx, &stopTimeout))
	check()
	require.NoError(t, remote.Stop(ctx, &stopTimeout))
	failedCtx, failedCancel := context.WithTimeout(ctx, 5*time.Second)
	defer failedCancel()
	stream, err := dumper.Dump(failedCtx)
	if err != nil {
		return
	}
	_, _ = io.ReadAll(stream)
	require.Error(t, stream.Close(), "a missing authoritative source must fail rather than select a local container")
}
