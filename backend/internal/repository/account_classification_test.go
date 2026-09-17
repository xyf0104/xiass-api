package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListAccountIDsByClassificationUsesEffectiveCredentialOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectQuery("WITH effective_accounts AS \\([\\s\\S]+LEFT JOIN accounts AS owner[\\s\\S]+a.parent_account_id[\\s\\S]+ORDER BY id").
		WithArgs(
			service.OpenAIOAuthCredentialSourceIDExtraKey,
			service.PlatformOpenAI,
			service.AccountTypeOAuth,
			service.AccountSubscriptionPlanPlus,
			service.AccountLoginMethodPassword2FA,
			service.OpenAIOAuthReauthorizationEmailCredentialKey,
			service.OpenAIOAuthReauthorizationPasswordCredentialKey,
			service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey,
			service.OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(17).AddRow(18).AddRow(19))

	accountIDs, err := repo.ListAccountIDsByClassification(
		context.Background(),
		service.AccountSubscriptionPlanPlus,
		service.AccountLoginMethodPassword2FA,
	)
	require.NoError(t, err)
	require.Equal(t, []int64{17, 18, 19}, accountIDs)
	require.NoError(t, mock.ExpectationsWereMet())
}
