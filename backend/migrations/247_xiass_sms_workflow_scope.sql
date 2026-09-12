-- Isolated batch OAuth reservations must never become normal receiver sessions.
ALTER TABLE xiass_sms_card_keys ADD COLUMN workflow_scope VARCHAR(128) NOT NULL DEFAULT '';

DROP INDEX uq_xiass_sms_card_keys_active_owner;
CREATE UNIQUE INDEX uq_xiass_sms_card_keys_active_owner
    ON xiass_sms_card_keys(owner_user_id)
    WHERE status = 'active' AND owner_user_id IS NOT NULL AND workflow_scope = '';
CREATE UNIQUE INDEX uq_xiass_sms_card_keys_active_workflow
    ON xiass_sms_card_keys(owner_user_id, workflow_scope)
    WHERE status = 'active' AND owner_user_id IS NOT NULL AND workflow_scope <> '';

-- Every existing settlement path returns reusable keys to the ordinary queue.
CREATE FUNCTION xiass_sms_clear_workflow_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status <> 'active' THEN
        NEW.workflow_scope := '';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER xiass_sms_clear_workflow_scope
    BEFORE UPDATE ON xiass_sms_card_keys
    FOR EACH ROW EXECUTE FUNCTION xiass_sms_clear_workflow_scope();
