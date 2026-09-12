package migrations

import (
	"strings"
	"testing"
)

func TestSMSWorkflowScopeMigrationPreservesExistingReservations(t *testing.T) {
	raw, err := FS.ReadFile("247_xiass_sms_workflow_scope.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, fragment := range []string{"NOT NULL DEFAULT ''", "uq_xiass_sms_card_keys_active_owner", "uq_xiass_sms_card_keys_active_workflow", "workflow_scope = ''", "workflow_scope <> ''", "IF NEW.status <> 'active'", "BEFORE UPDATE"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("missing migration invariant %q", fragment)
		}
	}
	for _, forbidden := range []string{"DELETE FROM", "TRUNCATE", "DROP TABLE", "UPDATE xiass_sms_card_keys SET"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must preserve existing reservations: %s", forbidden)
		}
	}
}
