BEGIN;

CREATE TABLE credential_maintenance (
    tenant_id bigint NOT NULL,
    credential_id bigint NOT NULL,
    approved_by bigint NOT NULL,
    owner_user_id bigint NOT NULL,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    enabled boolean NOT NULL DEFAULT true,
    approved_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, credential_id),
    FOREIGN KEY (tenant_id, credential_id) REFERENCES shop_credential (tenant_id, id) ON DELETE RESTRICT
);

CREATE TABLE credential_refresh_invocation (
    run_id bigint PRIMARY KEY,
    tenant_id bigint NOT NULL,
    credential_id bigint NOT NULL,
    shop_id bigint NOT NULL,
    owner_user_id bigint NOT NULL,
    actor_id bigint,
    maintenance_generation bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, credential_id, run_id) REFERENCES credential_refresh_run (tenant_id, credential_id, id) ON DELETE RESTRICT
);

ALTER TABLE sync_job ADD COLUMN credential_blocked_id bigint;
ALTER TABLE sync_job ADD COLUMN credential_blocked_version bigint;
ALTER TABLE sync_job ADD COLUMN credential_blocked_at timestamptz;
ALTER TABLE sync_job ADD COLUMN block_reason text NOT NULL DEFAULT '' CHECK (block_reason IN ('','credential_blocked','admin_disabled'));
ALTER TABLE sync_run ADD COLUMN credential_blocked_id bigint;
ALTER TABLE sync_run ADD COLUMN credential_used_id bigint;

CREATE FUNCTION maintain_sync_block_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT NEW.enabled THEN NEW.block_reason := 'admin_disabled';
    ELSIF NEW.credential_blocked_id IS NOT NULL THEN NEW.block_reason := 'credential_blocked';
    ELSE NEW.block_reason := ''; END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER sync_block_owner BEFORE INSERT OR UPDATE ON sync_job FOR EACH ROW EXECUTE FUNCTION maintain_sync_block_owner();
UPDATE sync_job SET block_reason='admin_disabled' WHERE NOT enabled;

CREATE FUNCTION revoke_credential_maintenance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.owner_user_id <> OLD.owner_user_id OR NEW.dept_id <> OLD.dept_id OR NEW.status='disabled' OR NEW.deleted_at IS NOT NULL THEN
        UPDATE credential_maintenance SET enabled=false,generation=generation+1
        WHERE tenant_id=NEW.tenant_id AND credential_id IN (SELECT id FROM shop_credential WHERE tenant_id=NEW.tenant_id AND shop_id=NEW.id);
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER revoke_shop_maintenance AFTER UPDATE ON shop FOR EACH ROW EXECUTE FUNCTION revoke_credential_maintenance();

ALTER TABLE notification ADD COLUMN read_at timestamptz;
ALTER TABLE notification ADD COLUMN available_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE notification ADD COLUMN shop_id bigint;
ALTER TABLE notification ADD COLUMN credential_id bigint;

CREATE TABLE credential_notification_dedup (
    tenant_id bigint NOT NULL,
    event_key text NOT NULL,
    notified_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, event_key)
);

CREATE TABLE credential_notification_setting (
    tenant_id bigint PRIMARY KEY,
    email_enabled boolean NOT NULL DEFAULT false,
    webhook_enabled boolean NOT NULL DEFAULT false,
    cipher_text bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version integer NOT NULL CHECK (key_version > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMIT;
