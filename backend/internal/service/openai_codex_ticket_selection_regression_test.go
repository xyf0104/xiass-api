package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type ticketSelectionExitFixture struct {
	model  string
	blocks int
	delay  time.Duration
	state  string
}

type ticketSelectionRegressionUpstream struct {
	HTTPUpstream
	exits       map[string]ticketSelectionExitFixture
	requested   string
	lastURL     string
	finishLast  <-chan struct{}
	lastWaiting chan struct{}
	completed   chan string
	mu          sync.Mutex
	calls       map[string]int
}

func (u *ticketSelectionRegressionUpstream) DoWithTLS(req *http.Request, proxyURL string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	exit, ok := u.exits[proxyURL]
	if !ok {
		return nil, fmt.Errorf("unexpected fixture proxy")
	}
	u.mu.Lock()
	u.calls[proxyURL]++
	u.mu.Unlock()
	timer := time.NewTimer(exit.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	// Created echoes the request; only completed identifies the observed model.
	prefix := "data: {\"type\":\"response.created\",\"response\":{\"model\":" + jsonString(u.requested) + "}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n"
	suffix := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":" + jsonString(exit.model) + "}}\n\n"
	body := &ticketSelectionRegressionBody{
		ctx: req.Context(), prefix: strings.NewReader(prefix), suffix: strings.NewReader(suffix),
		onComplete: func() { u.completed <- proxyURL },
	}
	if proxyURL == u.lastURL {
		body.gate, body.waiting = u.finishLast, u.lastWaiting
	}
	header := make(http.Header)
	header.Set(openAICodexTurnStateHeader, exit.state)
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: body}, nil
}

type ticketSelectionRegressionBody struct {
	ctx        context.Context
	prefix     *strings.Reader
	suffix     *strings.Reader
	gate       <-chan struct{}
	waiting    chan struct{}
	admitted   bool
	onComplete func()
	once       sync.Once
}

func (b *ticketSelectionRegressionBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	if !b.admitted {
		b.admitted = true
		if b.gate != nil {
			close(b.waiting)
			select {
			case <-b.gate:
			case <-b.ctx.Done():
				return 0, b.ctx.Err()
			}
		}
	}
	n, err := b.suffix.Read(p)
	if err == io.EOF {
		b.once.Do(b.onComplete)
	}
	return n, err
}

func (b *ticketSelectionRegressionBody) Close() error { return nil }

func TestOpenAICodexTicketSelectionRegression(t *testing.T) {
	const astra = "gpt-6-astra"
	const luna = "gpt-5.6-luna"
	cases := []struct {
		name     string
		exits    []ticketSelectionExitFixture
		winner   int // One-based proxy ID; zero means no eligible candidate.
		fallback bool
	}{
		{
			name: "312_astra_beats_292_astra_despite_faster_luna",
			exits: []ticketSelectionExitFixture{
				{model: astra, blocks: 10, delay: 250 * time.Millisecond},
				{model: luna, blocks: 11, delay: 5 * time.Millisecond},
				{model: astra, blocks: 11, delay: 80 * time.Millisecond},
			},
			winner: 3,
		},
		{
			name: "all_312_luna_fastest_last_exit_is_shared_and_not_blocked",
			exits: []ticketSelectionExitFixture{
				{model: luna, blocks: 11, delay: 250 * time.Millisecond},
				{model: luna, blocks: 11, delay: 140 * time.Millisecond},
				{model: luna, blocks: 11, delay: 5 * time.Millisecond},
			},
			winner: 3, fallback: true,
		},
		{
			name: "faster_other_model_cannot_replace_luna",
			exits: []ticketSelectionExitFixture{
				{model: "gpt-5.6-sol", blocks: 11, delay: 5 * time.Millisecond},
				{model: "gpt-5.6-terra", blocks: 10, delay: 10 * time.Millisecond},
				{model: luna, blocks: 11, delay: 80 * time.Millisecond},
			},
			winner: 3, fallback: true,
		},
		{
			name: "only_other_models_produce_no_candidate",
			exits: []ticketSelectionExitFixture{
				{model: "gpt-5.6-sol", blocks: 11, delay: 5 * time.Millisecond},
				{model: "gpt-5.6-terra", blocks: 10, delay: 10 * time.Millisecond},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			t.Cleanup(cancel)
			gate := make(chan struct{})
			upstream := &ticketSelectionRegressionUpstream{
				exits: make(map[string]ticketSelectionExitFixture), requested: astra,
				finishLast: gate, lastWaiting: make(chan struct{}),
				completed: make(chan string, len(tc.exits)), calls: make(map[string]int),
			}
			account := ticketTestAccount(950)
			account.Credentials["plan_type"] = "plus"
			account.MultiProxyConfigured = true
			issued := time.Now().Truncate(time.Second)
			for i, exit := range tc.exits {
				exit.state = syntheticCodexTicketState(exit.blocks, issued, byte(i+1))
				proxy := &Proxy{ID: int64(i + 1), Name: fmt.Sprintf("fixture-%d", i+1), Protocol: "http", Host: "127.0.0.1", Port: 19000 + i, Status: StatusActive}
				account.ProxyBindings = append(account.ProxyBindings, AccountProxyBinding{ProxyID: proxy.ID, Proxy: proxy, MaxConcurrency: 1})
				upstream.exits[proxy.URL()] = exit
				if i == len(tc.exits)-1 {
					upstream.lastURL = proxy.URL()
				}
			}
			repo := &codexTicketRefreshRepo{accounts: []Account{*account}}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{
				Enabled: true, FailClosed: true, TTLSeconds: 3600, Models: []string{astra},
			}, upstream)
			svc.accountRepo = repo
			type result struct {
				statuses []OpenAICodexTicketStatus
				err      error
			}
			done := make(chan result, 1)
			workerDone := make(chan struct{})
			go func() {
				defer close(workerDone)
				statuses, err := svc.RefreshOpenAICodexTicket(ctx, account.ID, astra)
				done <- result{statuses: statuses, err: err}
			}()
			t.Cleanup(func() { cancel(); <-workerDone })
			select {
			case <-upstream.lastWaiting:
			case <-ctx.Done():
				t.Fatal("last exit did not reach its completion gate")
			}
			for i := 0; i < len(tc.exits)-1; i++ {
				select {
				case url := <-upstream.completed:
					require.NotEqual(t, upstream.lastURL, url)
				case <-ctx.Done():
					t.Fatal("an earlier exit did not complete")
				}
			}
			select {
			case <-done:
				t.Fatal("refresh returned before the last exit completed")
			default:
			}
			close(gate)
			outcome := <-done
			require.NoError(t, ctx.Err())
			require.Len(t, upstream.completed, 1, "the last response must also be fully consumed")
			upstream.mu.Lock()
			calls := make(map[string]int, len(upstream.calls))
			for url, count := range upstream.calls {
				calls[url] = count
			}
			upstream.mu.Unlock()
			require.Len(t, calls, len(tc.exits))
			for _, count := range calls {
				require.Equal(t, 1, count)
			}
			if tc.winner == 0 {
				require.Error(t, outcome.err)
				require.Nil(t, svc.lookupOpenAICodexTicket(account, astra))
				require.True(t, svc.openAICodexTicketBlocksAccount(account, astra))
				require.Empty(t, repo.updates)
				return
			}
			require.NoError(t, outcome.err)
			require.Len(t, outcome.statuses, 1)
			status := outcome.statuses[0]
			require.True(t, status.Ready)
			require.False(t, status.Blocked)
			require.EqualValues(t, tc.winner, status.ProxyID)
			require.Equal(t, tc.fallback, status.Fallback)
			require.Equal(t, tc.exits[tc.winner-1].model, status.ObservedModel)
			require.Equal(t, 312, status.Length)
			require.Len(t, status.Probes, len(tc.exits))
			for _, probe := range status.Probes {
				require.True(t, probe.Completed)
			}
			if !tc.fallback {
				require.Less(t, status.Probes[1].LatencyMs, status.LatencyMs, "Luna actually produced output before the winning Astra")
				require.Less(t, status.LatencyMs, status.Probes[0].LatencyMs, "312 Astra actually produced output before 292 Astra")
			} else if tc.exits[0].model == luna {
				for _, probe := range status.Probes[:len(status.Probes)-1] {
					require.Less(t, status.LatencyMs, probe.LatencyMs)
				}
			}
			winner := upstream.exits[account.ProxyBindings[tc.winner-1].Proxy.URL()]
			stored := parseOpenAICodexTicketFromAny(account.ID, astra, repo.updates[openAICodexTicketExtraKey(astra)])
			require.NotNil(t, stored)
			require.Equal(t, winner.state, stored.State)
			require.Equal(t, len(tc.exits), stored.Attempts)
			// Reload persisted state in a fresh service, then use every business exit.
			account.Extra[openAICodexTicketExtraKey(astra)] = stored
			restarted := ticketTestService(t, svc.openAICodexTicketConfig(), nil)
			for _, binding := range account.ProxyBindings {
				businessAccount := *account
				businessAccount.RequestProxy = binding.Proxy
				require.False(t, restarted.openAICodexTicketBlocksAccount(&businessAccount, astra))
				headers := make(http.Header)
				require.NoError(t, restarted.applyOpenAICodexTicket(ctx, &businessAccount, astra, headers))
				require.Equal(t, winner.state, headers.Get(openAICodexTurnStateHeader))
			}
		})
	}
}
