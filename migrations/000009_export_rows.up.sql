BEGIN;
ALTER TABLE export_job ADD COLUMN attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0);
ALTER TABLE export_job ADD COLUMN row_total bigint NOT NULL DEFAULT 0 CHECK (row_total >= 0);
CREATE TABLE export_job_row (
    export_id bigint NOT NULL,
    attempt integer NOT NULL CHECK (attempt > 0),
    ordinal bigint NOT NULL CHECK (ordinal > 0),
    projection jsonb NOT NULL,
    sort_key jsonb NOT NULL,
    generated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (export_id, attempt, ordinal)
);
COMMIT;
