BEGIN;
ALTER TABLE file_object DROP COLUMN provider_id;
DROP TABLE storage_provider;
UPDATE file_object SET purpose = 'product_image' WHERE purpose IN ('attachment', 'shop');
ALTER TABLE file_object DROP CONSTRAINT file_object_purpose_check;
ALTER TABLE file_object ADD CONSTRAINT file_object_purpose_check
    CHECK (purpose IN ('product_image', 'export', 'snapshot'));
COMMIT;
