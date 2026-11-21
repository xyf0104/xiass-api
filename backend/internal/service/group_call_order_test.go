package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestGroupCallOrderAcrossSchedulers(t *testing.T) {
	cheap := openAIModelRoutingTestAccount(621, 33, 3, "")
	other := openAIModelRoutingTestAccount(622, 33, 3, "")
	cheap.GroupIDs, other.GroupIDs = []int64{33, 47}, []int64{33, 47}
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		for _, tc := range []struct {
			name  string
			group int64
			order []int64
			busy  bool
			want  int64
		}{
			{"cheap_first", 33, []int64{621, 622}, false, 621},
			{"reverse_other_group", 47, []int64{622, 621}, false, 622},
			{"first_full", 33, []int64{621, 622}, true, 622},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: tc.group, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AccountCallOrder: tc.order})
				got := selectOpenAIModelRoutingTestAccount(t, mode, ctx, tc.group, []Account{other, cheap}, schedulerTestConcurrencyCache{acquireResults: map[int64]bool{621: !tc.busy, 622: true}}, "")
				require.Equal(t, tc.want, got.Account.ID)
				require.True(t, got.Acquired)
				got.ReleaseFunc()
			})
		}
	}
}

func TestGroupCallOrderGenericFallbackAndIsolation(t *testing.T) {
	g := &Group{ID: 33, AccountCallOrder: []int64{621, 622}}
	ctx := context.WithValue(context.Background(), ctxkey.Group, g)
	accounts := []Account{{ID: 9, Priority: 0}, {ID: 622, Priority: 3}, {ID: 621, Priority: 8}, {ID: 10, Priority: -1}}
	for _, filter := range []func(context.Context, *int64, []Account) ([]Account, error){(&GatewayService{}).filterAccountCandidates, (&GeminiMessagesCompatService{}).filterAccountCandidates} {
		ordered, err := filter(ctx, &g.ID, accounts)
		require.NoError(t, err)
		require.Less(t, ordered[2].Priority, ordered[1].Priority)
		require.Less(t, ordered[1].Priority, ordered[3].Priority)
		require.Less(t, ordered[3].Priority, ordered[0].Priority)
		other := int64(44)
		original, err := filter(ctx, &other, accounts)
		require.NoError(t, err)
		require.Equal(t, accounts, original)
		require.Equal(t, 8, accounts[2].Priority)
	}
}
