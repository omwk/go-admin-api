BEGIN;
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
