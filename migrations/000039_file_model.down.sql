BEGIN;
ALTER TABLE storage_provider DROP COLUMN quota_per_user_mb;
ALTER TABLE storage_provider DROP COLUMN access_mode;
ALTER TABLE file_object DROP COLUMN thumb_key;
ALTER TABLE file_object DROP COLUMN category;
ALTER TABLE file_object DROP CONSTRAINT file_object_purpose_check;
UPDATE file_object SET purpose = 'attachment' WHERE purpose = 'avatar';
ALTER TABLE file_object ADD CONSTRAINT file_object_purpose_check
    CHECK (purpose IN ('product_image', 'attachment', 'export', 'shop', 'snapshot'));
COMMENT ON COLUMN file_object.purpose IS NULL;
COMMENT ON COLUMN sys_user.avatar IS NULL;
COMMIT;
