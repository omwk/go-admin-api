BEGIN;
DROP TABLE export_job_row;
ALTER TABLE export_job DROP COLUMN row_total;
ALTER TABLE export_job DROP COLUMN attempt;
COMMIT;
