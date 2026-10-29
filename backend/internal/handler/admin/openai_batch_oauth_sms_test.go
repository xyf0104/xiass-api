package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBatchOAuthSMSProviderControlMetadataIsPreserved(t *testing.T) {
	for _, action := range []string{"check", "cancel"} {
		t.Run(action, func(t *testing.T) {
			f := newBatchOAuthFixture(t)
			f.status, f.stage = "running", "sms_waiting"
			r, id := startFixtureTask(t, f)
			f.h.batchSMSService = &batchSMSStub{session: "private-session", result: &service.PixlabSMSResult{
				Status: "WAITING", SessionID: "private-session", PollSeconds: 9,
				NumberPending: true, LocalStop: action == "cancel",
			}}
			method, path, body := "GET", "/tasks/"+id+"/sms", ""
			if action == "cancel" {
				method, path, body = "POST", path+"/cancel", `{"confirmed":true}`
			}
			w := batchOAuthRequest(r, method, path, body)
			require.Equal(t, http.StatusOK, w.Code)
			var envelope struct {
				Data struct {
					PollSeconds   int  `json:"poll_seconds"`
					NumberPending bool `json:"number_pending"`
					LocalStop     bool `json:"local_stop"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.Equal(t, 9, envelope.Data.PollSeconds)
			require.True(t, envelope.Data.NumberPending)
			require.Equal(t, action == "cancel", envelope.Data.LocalStop)
			require.NotContains(t, w.Body.String(), "private-session")
		})
	}
}

func TestBatchOAuthSMSDeliveryFailureRemainsTerminal(t *testing.T) {
	for _, failure := range []string{"rejected", "accepted_response_lost", "wrong_attempt", "wrong_owner", "malformed_response", "cancel_failed"} {
		t.Run(failure, func(t *testing.T) {
			f := newBatchOAuthFixture(t)
			f.status, f.stage = "running", "sms_waiting"
			r, id := startFixtureTask(t, f)
			task := f.h.batchOAuthStore.tasks[id]
			sms := &batchSMSStub{session: "consumed-reservation"}
			sms.action = func(owner int64, scope, session, action string, confirmed bool) (*service.PixlabSMSResult, error) {
				require.Equal(t, int64(42), owner)
				require.Equal(t, id, scope)
				require.Equal(t, "consumed-reservation", session)
				require.Equal(t, "check", action)
				sms.session = ""
				return &service.PixlabSMSResult{Status: "RECEIVED", Code: "123456"}, nil
			}
			f.h.batchSMSService = sms
			var deliveries, cancellations atomic.Int32
			f.mu.Lock()
			f.sidecarAction = func(w http.ResponseWriter, req *http.Request, attempt string, payload map[string]any) bool {
				switch {
				case strings.HasSuffix(req.URL.Path, "/sms-code"):
					deliveries.Add(1)
					switch failure {
					case "wrong_attempt", "wrong_owner":
						owner := 42
						if failure == "wrong_attempt" {
							attempt += "-old"
						} else {
							owner = 999
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"id": attempt, "owner_id": owner, "status": "running", "stage": "sms_waiting"})
					case "malformed_response":
						_, _ = w.Write([]byte("{"))
					default:
						if failure == "accepted_response_lost" {
							f.stage = "profile"
						}
						http.Error(w, "private-sidecar-detail", http.StatusBadGateway)
					}
					return true
				case strings.HasSuffix(req.URL.Path, "/cancel"):
					cancellations.Add(1)
					if failure == "cancel_failed" {
						http.Error(w, "private-cancel-detail", http.StatusServiceUnavailable)
						return true
					}
				}
				return false
			}
			f.mu.Unlock()

			w := batchOAuthRequest(r, "GET", "/tasks/"+id+"/sms", "")
			require.Equal(t, http.StatusBadGateway, w.Code)
			require.NotContains(t, w.Body.String(), "123456")
			require.NotContains(t, w.Body.String(), "private-")
			require.Equal(t, "failed", task.Status)
			require.Equal(t, "failed", task.Stage)
			require.Equal(t, "sms_delivery_failed", task.Reason)
			require.Equal(t, task.Reason, batchOAuthPublicReason(task.Reason))
			require.False(t, task.RequiresSMSConfirmation)
			require.NotNil(t, task.FinishedAt)
			require.Empty(t, task.loginPasswordEncrypted)
			require.Empty(t, task.loginTOTPEncrypted)
			require.Contains(t, string(task.cachedSnapshotJSON()), `"reason":"sms_delivery_failed"`)
			require.NotContains(t, string(task.cachedSnapshotJSON()), "123456")

			// No later sidecar state, including success, can resurrect this attempt.
			beforePoll := f.sidecarCalls.Load()
			for _, status := range []string{"running", "canceled", "completed"} {
				f.mu.Lock()
				f.status, f.stage = status, "callback_received"
				f.mu.Unlock()
				w = batchOAuthRequest(r, "GET", "/tasks/"+id, "")
				require.Equal(t, http.StatusOK, w.Code)
				require.Contains(t, w.Body.String(), `"reason":"sms_delivery_failed"`)
			}
			require.Equal(t, beforePoll, f.sidecarCalls.Load())
			require.Equal(t, http.StatusConflict, batchOAuthRequest(r, "GET", "/tasks/"+id+"/sms", "").Code)
			require.Equal(t, http.StatusConflict, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
			require.Equal(t, 1, sms.calls)
			require.Equal(t, int32(1), deliveries.Load())
			require.Equal(t, int32(1), cancellations.Load())
			require.Zero(t, f.client.calls.Load())
			oldAttempt, oldSession := task.sidecarID, task.sessionID
			_, err := f.h.openaiOAuthService.ExchangeWorkflowCode(context.Background(), &service.OpenAIExchangeCodeInput{
				SessionID: oldSession, State: task.state, Code: "late-fixture-callback",
			})
			require.Equal(t, "OPENAI_OAUTH_SESSION_NOT_FOUND", infraerrors.Reason(err))
			require.Zero(t, f.client.calls.Load())

			f.mu.Lock()
			f.sidecarAction = nil
			f.status, f.stage = "running", "phone_required"
			f.mu.Unlock()
			w = batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"confirmed":true,"password":"synthetic-restart-password"}`)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, id, task.ID)
			require.NotEqual(t, oldAttempt, task.sidecarID)
			require.NotEqual(t, oldSession, task.sessionID)
			require.Equal(t, 1, task.RestartCount)
			require.False(t, task.terminal())
			require.Equal(t, 1, sms.calls, "restart must not replay the consumed reservation")
			f.mu.Lock()
			f.sidecarAction = func(w http.ResponseWriter, req *http.Request, _ string, _ map[string]any) bool {
				if req.Method != http.MethodGet {
					return false
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": oldAttempt, "owner_id": 42, "status": "completed", "stage": "callback_received"})
				return true
			}
			f.mu.Unlock()
			require.Equal(t, http.StatusBadGateway, batchOAuthRequest(r, "GET", "/tasks/"+id, "").Code)
			require.Equal(t, http.StatusConflict, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
			require.Zero(t, f.client.calls.Load())
		})
	}
}

func TestBatchOAuthSMSMissingReservationEmptyQueueKeepsConfirmedRetryAvailable(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.status, f.stage, f.reason = "running", "phone_required", "phone_rejected"
	r, id := startFixtureTask(t, f)
	sms := &batchSMSStub{}
	sms.action = func(owner int64, scope, session, action string, confirmed bool) (*service.PixlabSMSResult, error) {
		require.Empty(t, session)
		require.Equal(t, "change", action)
		require.True(t, confirmed)
		if sms.calls == 1 {
			return nil, fmt.Errorf("private-provider-detail: %w", service.ErrPixlabSMSNoCardKey)
		}
		return &service.PixlabSMSResult{Status: "WAITING", Number: "+12025550124", SessionID: "replacement"}, nil
	}
	f.h.batchSMSService = sms
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/change", `{"confirmed":true}`)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "SMS_CARD_KEY_UNAVAILABLE")
	require.NotContains(t, w.Body.String(), "private-provider-detail")
	require.NotContains(t, w.Body.String(), "retained")
	require.False(t, f.h.batchOAuthStore.tasks[id].terminal())
	require.Equal(t, http.StatusBadRequest, batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/change", `{"confirmed":false}`).Code)
	require.Equal(t, 1, sms.calls)
	w = batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/change", `{"confirmed":true}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 2, sms.calls)
}

func TestBatchOAuthSMSFailurePublishesSnapshotBeforeBrowserCleanup(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.status, f.stage = "running", "sms_waiting"
	r, id := startFixtureTask(t, f)
	f.h.batchSMSService = &batchSMSStub{session: "reserved", result: &service.PixlabSMSResult{Status: "RECEIVED", Code: "123456"}}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	f.mu.Lock()
	f.sidecarAction = func(w http.ResponseWriter, req *http.Request, _ string, _ map[string]any) bool {
		if strings.HasSuffix(req.URL.Path, "/sms-code") {
			w.WriteHeader(http.StatusBadGateway)
			return true
		}
		if strings.HasSuffix(req.URL.Path, "/cancel") {
			close(entered)
			<-release
		}
		return false
	}
	f.mu.Unlock()
	done := make(chan int, 1)
	go func() { done <- batchOAuthRequest(r, "GET", "/tasks/"+id+"/sms", "").Code }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("browser cleanup did not start")
	}
	w := batchOAuthRequest(r, "GET", "/tasks", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"reason":"sms_delivery_failed"`)
	// Unblock before test server cleanup while retaining the deferred release.
	release <- struct{}{}
	select {
	case status := <-done:
		require.Equal(t, http.StatusBadGateway, status)
	case <-time.After(time.Second):
		t.Fatal("SMS request did not finish")
	}
}

func TestBatchOAuthSMSLateCodeFromRejectedNumberAllowsConfirmedRetry(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.status, f.stage, f.reason = "running", "phone_required", "phone_rejected"
	r, id := startFixtureTask(t, f)
	task := f.h.batchOAuthStore.tasks[id]
	task.submittedPhone = "+12025550123"
	sms := &batchSMSStub{session: "rejected-reservation"}
	sms.action = func(owner int64, scope, session, action string, confirmed bool) (*service.PixlabSMSResult, error) {
		require.Equal(t, int64(42), owner)
		require.Equal(t, id, scope)
		require.Equal(t, "change", action)
		require.True(t, confirmed)
		if sms.calls == 1 {
			require.Equal(t, "rejected-reservation", session)
			sms.session = ""
			return &service.PixlabSMSResult{Status: "RECEIVED", Number: task.submittedPhone, Code: "123456"}, nil
		}
		require.Empty(t, session)
		return &service.PixlabSMSResult{Status: "WAITING", Number: "+12025550124", SessionID: "replacement"}, nil
	}
	f.h.batchSMSService = sms
	var phones, codes atomic.Int32
	f.mu.Lock()
	f.sidecarAction = func(w http.ResponseWriter, req *http.Request, _ string, payload map[string]any) bool {
		if strings.HasSuffix(req.URL.Path, "/sms-code") {
			codes.Add(1)
		}
		if strings.HasSuffix(req.URL.Path, "/phone") {
			phones.Add(1)
			require.Equal(t, "+12025550124", payload["phone"])
			f.stage, f.reason = "sms_waiting", ""
		}
		return false
	}
	f.mu.Unlock()
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/change", `{"confirmed":true}`)
	require.Equal(t, http.StatusConflict, w.Code)
	require.NotContains(t, w.Body.String(), "123456")
	require.Zero(t, phones.Load())
	require.Zero(t, codes.Load())
	require.Equal(t, "phone_required", task.Stage)

	foreign := batchOAuthRouter(f.h, 99, "admin")
	require.Equal(t, http.StatusNotFound, batchOAuthRequest(foreign, "POST", "/tasks/"+id+"/sms/change", `{"confirmed":true}`).Code)
	require.Equal(t, http.StatusBadRequest, batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/change", `{"confirmed":false}`).Code)
	require.Equal(t, 1, sms.calls)
	w = batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/change", `{"confirmed":true}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 2, sms.calls)
	require.Equal(t, int32(1), phones.Load())
	require.Zero(t, codes.Load())
	require.Equal(t, "+12025550124", task.submittedPhone)
}

func TestBatchOAuthSMSDeliverySuccessDoesNotFailOrReplay(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.status, f.stage = "running", "sms_waiting"
	r, id := startFixtureTask(t, f)
	sms := &batchSMSStub{session: "consumed-reservation"}
	sms.action = func(_ int64, _, _, _ string, _ bool) (*service.PixlabSMSResult, error) {
		sms.session = ""
		return &service.PixlabSMSResult{Status: "RECEIVED", Code: "123456"}, nil
	}
	f.h.batchSMSService = sms
	var deliveries, cancellations atomic.Int32
	f.mu.Lock()
	f.sidecarAction = func(w http.ResponseWriter, req *http.Request, _ string, _ map[string]any) bool {
		if strings.HasSuffix(req.URL.Path, "/sms-code") {
			deliveries.Add(1)
		}
		if strings.HasSuffix(req.URL.Path, "/cancel") {
			cancellations.Add(1)
		}
		return false
	}
	f.mu.Unlock()
	for i := 0; i < 2; i++ {
		w := batchOAuthRequest(r, "GET", "/tasks/"+id+"/sms", "")
		require.Equal(t, http.StatusOK, w.Code)
		require.NotContains(t, w.Body.String(), "123456")
	}
	require.Equal(t, "running", f.h.batchOAuthStore.tasks[id].Status)
	require.Equal(t, 1, sms.calls)
	require.Equal(t, int32(1), deliveries.Load())
	require.Zero(t, cancellations.Load())
}

func TestBatchOAuthSMSDeliveryFailurePersistsSanitizedReauthorizationHistory(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.status, f.stage = "running", "sms_waiting"
	r, id := startFixtureTask(t, f)
	task := f.h.batchOAuthStore.tasks[id]
	task.Mode, task.TargetAccountID = batchOAuthModeReauthorization, 448
	account := reauthorizationAccount(448)
	f.admin.getAccountResult = account
	r.GET("/reauthorization/:task_id/sms", f.h.OpenAIReauthorizationSMSAction)
	f.h.batchSMSService = &batchSMSStub{session: "reserved", result: &service.PixlabSMSResult{Status: "RECEIVED", Code: "123456"}}
	f.mu.Lock()
	f.sidecarAction = func(w http.ResponseWriter, req *http.Request, _ string, _ map[string]any) bool {
		if req.Method == http.MethodPost {
			http.Error(w, "private-sidecar-detail", http.StatusBadGateway)
			return true
		}
		return false
	}
	f.mu.Unlock()
	w := batchOAuthRequest(r, "GET", "/reauthorization/"+id+"/sms", "")
	require.Equal(t, http.StatusBadGateway, w.Code)
	updated, err := f.admin.GetAccount(context.Background(), 448)
	require.NoError(t, err)
	state := service.OpenAIReauthorizationStateFromAccount(updated)
	require.Equal(t, service.OpenAIReauthorizationResultFailed, state.LastResult)
	require.Equal(t, "sms_delivery_failed", state.LastReason)
	require.NotNil(t, state.LastResultAt)
	require.Equal(t, account.Credentials, updated.Credentials)
	require.Equal(t, account.Schedulable, updated.Schedulable)
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "123456")
	require.NotContains(t, string(raw), "private-sidecar-detail")
}
