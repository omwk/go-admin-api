BEGIN;
DROP TABLE raw_partition_pin;
DROP INDEX uq_replay_active;
ALTER TABLE sync_run DROP COLUMN scheduling_generation, DROP COLUMN ready_at;
ALTER TABLE replay_run DROP COLUMN ready_at;
ALTER TABLE replay_run DROP CONSTRAINT replay_run_status_check;
ALTER TABLE replay_run ADD CONSTRAINT replay_run_status_check
    CHECK (status IN ('running','success','partial','failed')) NOT VALID;
UPDATE sync_job SET options=options-'_scheduler';
DROP INDEX idx_raw_replay_source;
DROP TABLE replay_run_item;
ALTER TABLE replay_run
    DROP COLUMN scheduling_generation,
    DROP COLUMN authorization_generation,
    DROP COLUMN function_version,
    DROP COLUMN mapping_spec,
    DROP COLUMN mapping_checksum,
    DROP COLUMN upper_key,
    DROP COLUMN checkpoint_key,
    DROP COLUMN processed,
    DROP COLUMN total;
-- Preserve existing evidence; the old whitelist applies to subsequent writes.
ALTER TABLE quarantine_record DROP CONSTRAINT quarantine_record_reason_code_check;
ALTER TABLE quarantine_record ADD CONSTRAINT quarantine_record_reason_code_check CHECK
    (reason_code IN ('missing_required','type_mismatch','invalid_value','mapping_error')) NOT VALID;
COMMIT;
