-- Existing cards retain the legacy provider. Provider runtime JSON is encrypted
-- by the application; this migration does not bind or modify individual cards.
ALTER TABLE xiass_sms_card_keys
    ADD COLUMN IF NOT EXISTS provider VARCHAR(16) NOT NULL DEFAULT 'pixlab',
    ADD COLUMN IF NOT EXISTS provider_state TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_pending BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE xiass_sms_card_keys
    DROP CONSTRAINT IF EXISTS xiass_sms_card_keys_provider_check;
ALTER TABLE xiass_sms_card_keys
    ADD CONSTRAINT xiass_sms_card_keys_provider_check CHECK (provider IN ('pixlab', 'giema'));

-- Preserve the existing workflow-scope trigger. This trigger sorts after it and
-- also covers inserts, so legacy release paths cannot queue unresolved sessions.
CREATE OR REPLACE FUNCTION xiass_sms_quarantine_pending_card() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.provider_pending AND NEW.status = 'queued' THEN
        NEW.status := 'cancelled';
        NEW.workflow_scope := '';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS z_xiass_sms_quarantine_pending_card ON xiass_sms_card_keys;
CREATE TRIGGER z_xiass_sms_quarantine_pending_card
    BEFORE INSERT OR UPDATE ON xiass_sms_card_keys
    FOR EACH ROW EXECUTE FUNCTION xiass_sms_quarantine_pending_card();
