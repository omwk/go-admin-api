BEGIN;
CREATE TABLE storage_provider (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenant(id),
    name text NOT NULL,
    endpoint text NOT NULL,
    port integer NOT NULL DEFAULT 443 CHECK (port BETWEEN 1 AND 65535),
    bucket text NOT NULL,
    use_ssl boolean NOT NULL DEFAULT true,
    region text NOT NULL DEFAULT 'us-east-1',
    access_key text NOT NULL,
    secret_enc bytea NOT NULL,
    public_base_url text NOT NULL DEFAULT '',
    is_default boolean NOT NULL DEFAULT false,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX storage_provider_default ON storage_provider(tenant_id) WHERE is_default;
ALTER TABLE file_object ADD COLUMN provider_id bigint REFERENCES storage_provider(id);
ALTER TABLE file_object DROP CONSTRAINT file_object_purpose_check;
ALTER TABLE file_object ADD CONSTRAINT file_object_purpose_check
    CHECK (purpose IN ('product_image', 'attachment', 'export', 'shop', 'snapshot'));
COMMIT;
