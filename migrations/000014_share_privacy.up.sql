BEGIN;
ALTER TABLE inquiry ADD COLUMN converted_at timestamptz, ADD COLUMN pii_purged_at timestamptz;
ALTER TABLE customer ADD COLUMN terminated_at timestamptz, ADD COLUMN pii_purged_at timestamptz;
UPDATE customer SET terminated_at=COALESCE(deleted_at,updated_at)
    WHERE status='disabled' OR deleted_at IS NOT NULL;
CREATE INDEX idx_inquiry_retention ON inquiry (tenant_id,created_at,id) WHERE pii_purged_at IS NULL;
CREATE INDEX idx_inquiry_converted ON inquiry (tenant_id,converted_at,id) WHERE pii_purged_at IS NULL AND converted_at IS NOT NULL;
CREATE INDEX idx_customer_retention ON customer (tenant_id,terminated_at,id) WHERE pii_purged_at IS NULL AND terminated_at IS NOT NULL;
CREATE INDEX idx_share_visit_retention ON share_visit (tenant_id,created_at,id);
CREATE TABLE share_inquiry_receipt (
    tenant_id bigint NOT NULL,
    share_link_id bigint NOT NULL,
    scope_hash text NOT NULL,
    key_hash text NOT NULL,
    request_hash text NOT NULL,
    inquiry_id bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,share_link_id,scope_hash,key_hash)
);
CREATE INDEX idx_share_inquiry_receipt_expiry ON share_inquiry_receipt (expires_at);
CREATE TABLE share_privacy_run (
    tenant_id bigint NOT NULL,
    run_day date NOT NULL,
    completed_at timestamptz,
    inquiries bigint NOT NULL DEFAULT 0,
    customers bigint NOT NULL DEFAULT 0,
    visits bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id,run_day)
);

-- Only explicitly approved share safety events may expire. The database clock
-- is authoritative even if a worker supplies a future cleanup time.
CREATE FUNCTION share_audit_retention_due(event_action text,event_resource text,event_created_at timestamptz)
RETURNS boolean LANGUAGE sql STABLE STRICT AS $$
    SELECT (
        (event_resource='share_link' AND event_action IN (
            'trade.share_link.create','trade.share_link.regenerate','trade.share_link.revoke',
            'share.link.lifecycle.revoke','share.visit.read'))
        OR (event_resource='inquiry' AND event_action IN (
            'share.inquiry.submit','share.inquiry.update','share.inquiry.pii.read'))
        OR (event_action='share.privacy.cleanup' AND event_resource IN (
            'inquiry','customer','share_visit','share_inquiry_receipt','audit_log'))
    ) AND event_created_at<=((CURRENT_TIMESTAMP AT TIME ZONE 'UTC')-INTERVAL '3 years') AT TIME ZONE 'UTC';
$$;
CREATE FUNCTION restrict_share_audit_retention() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT COALESCE(share_audit_retention_due(OLD.action,OLD.resource_type,OLD.created_at),false) THEN
        RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$;
DROP TRIGGER audit_log_append_only ON audit_log;
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_log_mutation();
CREATE TRIGGER audit_log_share_retention BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION restrict_share_audit_retention();
COMMIT;
