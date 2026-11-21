BEGIN;
ALTER TABLE file_object ADD COLUMN category text;
ALTER TABLE file_object ADD COLUMN thumb_key text;
ALTER TABLE file_object DROP CONSTRAINT file_object_purpose_check;
ALTER TABLE file_object ADD CONSTRAINT file_object_purpose_check
    CHECK (purpose IN ('product_image', 'attachment', 'avatar', 'export', 'shop', 'snapshot'));
ALTER TABLE storage_provider ADD COLUMN access_mode text NOT NULL DEFAULT 'public'
    CHECK (access_mode IN ('public', 'private'));
ALTER TABLE storage_provider ADD COLUMN quota_per_user_mb bigint NOT NULL DEFAULT 1024
    CHECK (quota_per_user_mb BETWEEN 0 AND 8796093022207);
COMMENT ON COLUMN file_object.purpose IS 'Internal source selected by the calling entry point';
COMMENT ON COLUMN sys_user.avatar IS 'Internal /api/v1/avatars/{user_id} path; legacy external URLs remain supported';
COMMIT;
