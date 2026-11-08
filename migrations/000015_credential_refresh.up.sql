BEGIN;

ALTER TABLE shop_credential DROP CONSTRAINT shop_credential_status_check;
UPDATE shop_credential SET status='expiring' WHERE status='expired';
ALTER TABLE shop_credential ADD CONSTRAINT shop_credential_status_check
    CHECK (status IN ('active','expiring','refreshing','needs_reauth','disabled'));
ALTER TABLE shop_credential ADD COLUMN refresh_generation bigint NOT NULL DEFAULT 0 CHECK (refresh_generation >= 0);
ALTER TABLE shop_credential ADD COLUMN retry_budget integer NOT NULL DEFAULT 5 CHECK (retry_budget BETWEEN 0 AND 5);

CREATE TABLE credential_refresh_run (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    credential_id bigint NOT NULL,
    input_version_id bigint NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    candidate_version integer NOT NULL CHECK (candidate_version > 1),
    staged_result_id bigint,
    output_version_id bigint,
    phase text NOT NULL CHECK (phase IN ('intent','sending','consumed','published','unknown')),
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running','success','failed','skipped')),
    failure_class text NOT NULL DEFAULT '' CHECK (failure_class IN ('','temporary','invalid','unknown')),
    reason text NOT NULL DEFAULT '' CHECK (reason IN ('','temporary','invalid','ambiguous','sending_lost','stage_failed','stage_expired','superseded')),
    attempt integer NOT NULL CHECK (attempt BETWEEN 1 AND 5),
    trigger text NOT NULL DEFAULT 'manual' CHECK (trigger IN ('manual','schedule','on_demand')),
    send_deadline timestamptz,
    next_retry_at timestamptz,
    started_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    finished_at timestamptz,
    trace_id text NOT NULL DEFAULT '',
    CONSTRAINT uq_refresh_run_identity UNIQUE (tenant_id, credential_id, id),
    CONSTRAINT uq_refresh_run_generation UNIQUE (tenant_id, credential_id, generation),
    CONSTRAINT uq_refresh_run_attempt UNIQUE (tenant_id, credential_id, input_version_id, attempt),
    CONSTRAINT fk_refresh_run_input FOREIGN KEY (tenant_id, credential_id, input_version_id)
        REFERENCES credential_version (tenant_id, credential_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_refresh_run_output FOREIGN KEY (tenant_id, credential_id, output_version_id)
        REFERENCES credential_version (tenant_id, credential_id, id) ON DELETE RESTRICT,
    CHECK ((status='running') = (finished_at IS NULL)),
    CHECK (phase <> 'published' OR (status='success' AND output_version_id IS NOT NULL)),
    CHECK (phase <> 'consumed' OR staged_result_id IS NOT NULL)
);
CREATE UNIQUE INDEX uq_refresh_run_pending ON credential_refresh_run (tenant_id, credential_id) WHERE status='running';
CREATE INDEX idx_cred_refresh_run ON credential_refresh_run (tenant_id, credential_id, started_at DESC);
CREATE INDEX idx_refresh_run_retry ON credential_refresh_run (next_retry_at) WHERE failure_class='temporary';

CREATE TABLE credential_refresh_stage (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    credential_id bigint NOT NULL,
    run_id bigint NOT NULL,
    version_no integer NOT NULL CHECK (version_no > 1),
    cipher_text bytea,
    nonce bytea,
    key_version integer NOT NULL CHECK (key_version > 0),
    aad text NOT NULL,
    fingerprint text NOT NULL,
    expires_at timestamptz NOT NULL,
    refresh_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    purge_after timestamptz NOT NULL,
    purged_at timestamptz,
    CONSTRAINT uq_refresh_stage_identity UNIQUE (tenant_id, credential_id, id),
    CONSTRAINT uq_refresh_stage_run UNIQUE (tenant_id, credential_id, run_id),
    CONSTRAINT fk_refresh_stage_run FOREIGN KEY (tenant_id, credential_id, run_id)
        REFERENCES credential_refresh_run (tenant_id, credential_id, id) ON DELETE RESTRICT,
    CHECK ((cipher_text IS NULL AND nonce IS NULL AND purged_at IS NOT NULL)
        OR (cipher_text IS NOT NULL AND nonce IS NOT NULL AND octet_length(cipher_text) >= 16 AND octet_length(nonce)=12 AND purged_at IS NULL)),
    CHECK (purge_after=created_at + interval '24 hours'),
    CHECK (refresh_at <= expires_at)
);
CREATE INDEX idx_refresh_stage_purge ON credential_refresh_stage (purge_after) WHERE purged_at IS NULL;
ALTER TABLE credential_refresh_run ADD CONSTRAINT fk_refresh_run_stage
    FOREIGN KEY (tenant_id, credential_id, staged_result_id)
    REFERENCES credential_refresh_stage (tenant_id, credential_id, id) ON DELETE RESTRICT;

CREATE TABLE credential_refresh_reconciliation (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL,
    credential_id bigint NOT NULL,
    run_id bigint NOT NULL,
    reason text NOT NULL CHECK (reason IN ('ambiguous','sending_lost','stage_failed','stage_expired','superseded')),
    created_at timestamptz NOT NULL,
    resolved_at timestamptz,
    CONSTRAINT uq_refresh_reconciliation_run UNIQUE (tenant_id, credential_id, run_id),
    CONSTRAINT fk_refresh_reconciliation_run FOREIGN KEY (tenant_id, credential_id, run_id)
        REFERENCES credential_refresh_run (tenant_id, credential_id, id) ON DELETE RESTRICT
);

COMMIT;
