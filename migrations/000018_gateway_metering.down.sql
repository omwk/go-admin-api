BEGIN;
ALTER TABLE api_call_log
    DROP CONSTRAINT ck_api_call_log_meter,
    DROP CONSTRAINT uq_api_call_log_call,
    DROP COLUMN finished_at,
    DROP COLUMN metered_units,
    DROP COLUMN result_hash,
    DROP COLUMN attempts,
    DROP COLUMN admitted,
    DROP COLUMN state,
    DROP COLUMN route_version,
    DROP COLUMN request_id,
    DROP COLUMN call_id;
DROP TABLE api_quota;
DROP TABLE api_call_meter;
DROP TABLE api_call;
COMMIT;
