package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func automaticReauthorizationTestAccount() *service.Account {
	return &service.Account{
		ID:       77,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":   "old-access",
			"refresh_token":  "old-refresh",
			"_token_version": int64(4),
		},
		Extra: map[string]any{service.OpenAIReauthorizationStateExtraKey: map[string]any{}},
	}
}

func automaticReauthorizationTestClaim() (service.OpenAIReauthorizationAutoClaim, service.OpenAIReauthorizationState) {
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	claim := service.OpenAIReauthorizationAutoClaim{
		EpisodeKey: "episode-77", ClaimID: "claim-77", BrowserMode: "server",
		ExecutionNodeID: "api", ClaimedAt: at, LeaseUntil: at.Add(45 * time.Minute),
	}
	state := service.OpenAIReauthorizationState{
		LastResult:     service.OpenAIReauthorizationResultRunning,
		LastInitiator:  service.OpenAIReauthorizationInitiatorAutomatic,
		AutoEpisodeKey: claim.EpisodeKey, AutoClaimID: claim.ClaimID,
		AutoBrowserMode: claim.BrowserMode, AutoExecutionNodeID: claim.ExecutionNodeID,
		AutoClaimedAt: &at, AutoLeaseUntil: func() *time.Time { v := claim.LeaseUntil; return &v }(),
	}
	return claim, state
}

func TestAutomaticReauthorizationRepositoryBindsClaimIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &accountRepository{sql: db}
	account := automaticReauthorizationTestAccount()
	claim, state := automaticReauthorizationTestClaim()
	stateJSON, err := openAIReauthorizationStateJSON(state)
	require.NoError(t, err)

	mock.ExpectExec(regexp.QuoteMeta(claimOpenAIReauthorizationAutoQuery)).
		WithArgs(openAIReauthorizationStateKey, stateJSON, claim.EpisodeKey, account.ID, account.Platform, account.Type,
			`{"_token_version":4,"access_token":"old-access","refresh_token":"old-refresh"}`, nil,
			service.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 1))
	applied, err := repo.ClaimOpenAIReauthorizationAutoAttempt(context.Background(), account, claim, state)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAutomaticReauthorizationRepositoryApplyReplacesCredentialsOnlyForClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &accountRepository{sql: db}
	account := automaticReauthorizationTestAccount()
	claim, state := automaticReauthorizationTestClaim()
	state.LastResult = service.OpenAIReauthorizationResultSuccess
	stateJSON, err := openAIReauthorizationStateJSON(state)
	require.NoError(t, err)
	newCredentials := `{"_token_version":5,"access_token":"new-access","refresh_token":"new-refresh"}`
	mock.ExpectExec(regexp.QuoteMeta(applyOpenAIReauthorizationAutoCredentialsQuery)).
		WithArgs(newCredentials, openAIReauthorizationStateKey, stateJSON, claim.ClaimID, account.ID, account.Platform, account.Type,
			`{"_token_version":4,"access_token":"old-access","refresh_token":"old-refresh"}`, false, service.StatusActive, nil,
			service.SchedulerOutboxEventAccountChanged).
		WillReturnResult(sqlmock.NewResult(0, 1))
	applied, err := repo.ApplyOpenAIReauthorizationAutoCredentials(context.Background(), account, claim,
		map[string]any{"_token_version": int64(5), "access_token": "new-access", "refresh_token": "new-refresh"}, state)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAutomaticReauthorizationRepositoryRejectsShadowAndMissingCredentials(t *testing.T) {
	repo := &accountRepository{}
	claim, state := automaticReauthorizationTestClaim()
	shadow := automaticReauthorizationTestAccount()
	shadow.ParentAccountID = func() *int64 { id := int64(1); return &id }()
	shadow.QuotaDimension = service.QuotaDimensionSpark
	_, err := repo.ClaimOpenAIReauthorizationAutoAttempt(context.Background(), shadow, claim, state)
	require.Error(t, err)
	missing := automaticReauthorizationTestAccount()
	delete(missing.Credentials, "access_token")
	_, err = repo.FinishOpenAIReauthorizationAutoAttempt(context.Background(), missing, claim, state)
	require.Error(t, err)
}
