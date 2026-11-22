BEGIN;
ALTER TABLE file_object DROP COLUMN thumb_attempts;
ALTER TABLE file_object DROP COLUMN thumb_status;
COMMIT;
