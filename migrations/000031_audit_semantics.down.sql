BEGIN;
CREATE OR REPLACE FUNCTION restrict_share_audit_retention() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT COALESCE(share_audit_retention_due(OLD.action,OLD.resource_type,OLD.created_at),false) THEN
        RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$;
ALTER TABLE audit_log
    DROP COLUMN error_message,
    DROP COLUMN error_code,
    DROP COLUMN resource_label,
    DROP COLUMN actor_tenant_name,
    DROP COLUMN actor_name;
-- Historical action normalization is intentionally irreversible.
COMMIT;
