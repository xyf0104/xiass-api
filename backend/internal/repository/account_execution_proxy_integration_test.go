//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionNodePreflightExplicitProxy(t *testing.T) {
	for _, tc := range []struct {
		name, marker, status string
		allowed              bool
	}{
		{"explicit", `"99"`, "active", true},
		{"missing", `null`, "active", false},
		{"stale", `"98"`, "active", false},
		{"wrong_type", `99`, "active", false},
		{"disabled", `"99"`, "disabled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tx, err := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			for _, query := range []string{
				`CREATE TEMP TABLE accounts (id bigint, proxy_id bigint, extra jsonb, updated_at timestamptz, deleted_at timestamptz) ON COMMIT DROP`,
				`CREATE TEMP TABLE proxies (id bigint, status text, deleted_at timestamptz, expires_at timestamptz) ON COMMIT DROP`,
				`CREATE TEMP TABLE scheduler_outbox (LIKE public.scheduler_outbox INCLUDING ALL) ON COMMIT DROP`,
				`INSERT INTO proxies (id,status) VALUES (84,'active'),(83,'active')`,
			} {
				_, err = tx.ExecContext(ctx, query)
				require.NoError(t, err)
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO proxies (id,status) VALUES (99,$1)`, tc.status)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, `INSERT INTO accounts (id,proxy_id,extra) VALUES (1,99,jsonb_build_object('xiass_execution_node_id','api2','xiass_execution_proxy_id',$1::jsonb))`, tc.marker)
			require.NoError(t, err)
			_, err = (&accountRepository{}).prepareExecutionNodeRoutingTx(ctx, tx, "api", 84, []string{"api", "api2"}, map[string]int64{"api": 84, "api2": 83})
			if tc.allowed {
				require.NoError(t, err)
				var proxyID int64
				require.NoError(t, tx.QueryRowContext(ctx, `SELECT proxy_id FROM accounts WHERE id=1`).Scan(&proxyID))
				require.Equal(t, int64(99), proxyID)
			} else {
				require.ErrorContains(t, err, "preflight failed")
			}
		})
	}
}
