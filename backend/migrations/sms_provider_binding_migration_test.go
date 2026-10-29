package migrations

import (
	"strings"
	"testing"
)

func TestSMSProviderBindingMigrationPreservesLegacyCardsAndQuarantinesPending(t *testing.T) {
	raw, err := FS.ReadFile("271_xiass_sms_provider_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, fragment := range []string{
		"provider VARCHAR(16) NOT NULL DEFAULT 'pixlab'",
		"provider_state TEXT NOT NULL DEFAULT ''",
		"provider_pending BOOLEAN NOT NULL DEFAULT false",
		"CHECK (provider IN ('pixlab', 'giema'))",
		"z_xiass_sms_quarantine_pending_card",
		"BEFORE INSERT OR UPDATE",
		"NEW.provider_pending AND NEW.status = 'queued'",
		"NEW.status := 'cancelled'",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("missing migration invariant %q", fragment)
		}
	}
	for _, forbidden := range []string{"DELETE FROM", "TRUNCATE", "DROP TABLE", "UPDATE xiass_sms_card_keys SET"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration must preserve existing card rows: %s", forbidden)
		}
	}
}
