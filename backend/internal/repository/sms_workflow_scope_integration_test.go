//go:build integration

package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSMSWorkflowScopePreservesNormalUniquenessAndIndependentTasks(t *testing.T) {
	tx := testTx(t)
	var owner int64
	require.NoError(t, tx.QueryRow(`INSERT INTO users(email,password_hash) VALUES($1,'test-only') RETURNING id`, uuid.NewString()+"@sms-scope.test").Scan(&owner))
	insert := func(scope string) error {
		session := uuid.NewString()
		hash := sha256.Sum256([]byte(session))
		_, err := tx.Exec(`INSERT INTO xiass_sms_card_keys(encrypted_key,key_fingerprint,status,owner_user_id,session_id,workflow_scope,consumed_at)
			VALUES('enc:test-only',$1,'active',$2,$3,$4,NOW())`, hex.EncodeToString(hash[:]), owner, session, scope)
		return err
	}
	require.NoError(t, insert(""))
	require.NoError(t, insert("workflow-00000001"))
	require.NoError(t, insert("workflow-00000002"))
	for _, scope := range []string{"", "workflow-00000001", "workflow-00000002"} {
		_, err := tx.Exec("SAVEPOINT duplicate_scope")
		require.NoError(t, err)
		require.Error(t, insert(scope), "same owner/scope must not double-claim")
		_, err = tx.Exec("ROLLBACK TO SAVEPOINT duplicate_scope")
		require.NoError(t, err)
	}
	var normalCount, total int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM xiass_sms_card_keys WHERE owner_user_id=$1 AND status='active' AND workflow_scope=''`, owner).Scan(&normalCount))
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM xiass_sms_card_keys WHERE owner_user_id=$1 AND status='active'`, owner).Scan(&total))
	require.Equal(t, 1, normalCount)
	require.Equal(t, 3, total)

	_, err := tx.Exec(`UPDATE xiass_sms_card_keys SET status='queued',owner_user_id=NULL,session_id=NULL WHERE owner_user_id=$1 AND workflow_scope='workflow-00000001'`, owner)
	require.NoError(t, err)
	var stale int
	require.NoError(t, tx.QueryRow(`SELECT count(*) FROM xiass_sms_card_keys WHERE workflow_scope='workflow-00000001'`).Scan(&stale))
	require.Zero(t, stale, "settlement trigger must clear scope on reusable cards")
	require.NoError(t, insert("workflow-00000001"))
}
