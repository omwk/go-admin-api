BEGIN;
ALTER TABLE file_object ADD COLUMN thumb_status text NOT NULL DEFAULT ''
    CHECK (thumb_status IN ('', 'pending', 'ready', 'failed'));
ALTER TABLE file_object ADD COLUMN thumb_attempts integer NOT NULL DEFAULT 0
    CHECK (thumb_attempts BETWEEN 0 AND 3);
UPDATE file_object SET thumb_status = 'ready' WHERE COALESCE(thumb_key, '') <> '';
COMMIT;
