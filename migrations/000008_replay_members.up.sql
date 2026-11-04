BEGIN;
-- Supports bounded directory expansion and unambiguous raw.id provenance checks.
CREATE INDEX idx_raw_replay_source ON raw_dedup
    (tenant_id, platform_id, shop_id, resource_type, raw_id, fetched_at);

ALTER TABLE quarantine_record DROP CONSTRAINT quarantine_record_reason_code_check;
ALTER TABLE quarantine_record ADD CONSTRAINT quarantine_record_reason_code_check CHECK
    (reason_code IN ('missing_required','type_mismatch','invalid_value','mapping_error','source_metadata_unknown','source_identity_ambiguous'));

ALTER TABLE replay_run
    ADD COLUMN total bigint NOT NULL DEFAULT 0 CHECK (total BETWEEN 0 AND 500000),
    ADD COLUMN processed bigint NOT NULL DEFAULT 0 CHECK (processed >= 0 AND processed <= total),
    ADD COLUMN checkpoint_key jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN upper_key jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN mapping_checksum text NOT NULL DEFAULT '',
    ADD COLUMN mapping_spec jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN function_version text NOT NULL DEFAULT '',
    ADD COLUMN authorization_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN scheduling_generation bigint NOT NULL DEFAULT 0;

CREATE TABLE replay_run_item (
    run_id bigint NOT NULL,
    fetched_at timestamptz NOT NULL,
    id bigint NOT NULL,
    external_id text NOT NULL,
    payload_sha256 text NOT NULL,
    outcome text CHECK (outcome IN ('inserted','updated','skipped_stale','skipped_same','quarantined','failed')),
    reason_code text NOT NULL DEFAULT '',
    disposed_at timestamptz,
    PRIMARY KEY (run_id, fetched_at, id),
    CHECK ((outcome IS NULL) = (disposed_at IS NULL))
);
COMMIT;
