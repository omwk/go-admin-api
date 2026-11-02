BEGIN;

CREATE TABLE raw_payload (
    id bigint GENERATED ALWAYS AS IDENTITY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL CHECK (resource_type IN ('product', 'order', 'inventory', 'category')),
    external_id text NOT NULL,
    api_name text NOT NULL DEFAULT '',
    request_meta jsonb NOT NULL DEFAULT '{}'::jsonb,
    payload jsonb NOT NULL,
    payload_sha256 text NOT NULL,
    payload_size integer NOT NULL CHECK (payload_size >= 0),
    mapping_version_id bigint,
    http_status integer,
    fetched_at timestamptz NOT NULL,
    PRIMARY KEY (id, fetched_at)
) PARTITION BY RANGE (fetched_at);
CREATE UNIQUE INDEX uq_raw_dedup ON raw_payload
    (tenant_id, platform_id, shop_id, resource_type, external_id, payload_sha256, fetched_at);
CREATE INDEX idx_raw_lookup ON raw_payload (tenant_id, resource_type, external_id, fetched_at DESC);
CREATE INDEX idx_raw_unmapped ON raw_payload (fetched_at) WHERE mapping_version_id IS NULL;

-- ADR-016 D1: retained for the entire online and replayable archive lifetime.
-- No raw FK: the catalog must survive partition detach/drop for archival.
CREATE TABLE raw_dedup (
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL,
    external_id text NOT NULL,
    payload_sha256 text NOT NULL,
    raw_id bigint NOT NULL,
    fetched_at timestamptz NOT NULL,
    CONSTRAINT uq_raw_directory UNIQUE
        (tenant_id, platform_id, shop_id, resource_type, external_id, payload_sha256)
);

-- INVOKER deliberately requires a separate maintenance login with DDL rights.
-- Pin the schema at migration time, independent of the caller's search_path.
DO $install$
BEGIN
    EXECUTE format($ddl$
        CREATE FUNCTION %1$I.ensure_raw_partition(month_text text) RETURNS boolean
        LANGUAGE plpgsql SECURITY INVOKER SET search_path = pg_catalog, %1$I AS $body$
        DECLARE
            month_start date;
            month_end date;
            partition_name text;
        BEGIN
            IF month_text IS NULL OR month_text !~ '^[0-9]{4}-(0[1-9]|1[0-2])$'
               OR left(month_text, 4) = '0000' OR month_text >= '9999-12' THEN
                RAISE EXCEPTION 'invalid partition month' USING ERRCODE = '22023';
            END IF;
            month_start := (month_text || '-01')::date;
            month_end := (month_start + interval '1 month')::date;
            partition_name := 'raw_payload_' || replace(month_text, '-', '');
            PERFORM pg_advisory_xact_lock(hashtext('%1$I.raw_payload'), 0);
            IF EXISTS (SELECT 1 FROM pg_inherits i
                JOIN pg_class c ON c.oid = i.inhrelid
                JOIN pg_namespace n ON n.oid = c.relnamespace
                WHERE i.inhparent = '%1$I.raw_payload'::regclass
                  AND n.nspname = %2$L AND c.relname = partition_name) THEN
                IF NOT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
                    WHERE n.nspname = %2$L AND c.relname = partition_name
                      AND pg_get_expr(c.relpartbound, c.oid) = format('FOR VALUES FROM (%%L) TO (%%L)',
                          (month_start::text || ' 00:00:00+00')::timestamptz,
                          (month_end::text || ' 00:00:00+00')::timestamptz)) THEN
                    RAISE EXCEPTION 'raw partition bounds mismatch' USING ERRCODE = '22023';
                END IF;
                RETURN false;
            END IF;
            EXECUTE format('CREATE TABLE %%I.%%I PARTITION OF %1$I.raw_payload FOR VALUES FROM (%%L) TO (%%L)',
                %2$L, partition_name, month_start::text || ' 00:00:00+00', month_end::text || ' 00:00:00+00');
            RETURN true;
        END;
        $body$
    $ddl$, current_schema(), current_schema());
    EXECUTE format('REVOKE ALL ON FUNCTION %I.ensure_raw_partition(text) FROM PUBLIC', current_schema());
    EXECUTE format('REVOKE CREATE ON SCHEMA %I FROM PUBLIC', current_schema());
END;
$install$;
SELECT ensure_raw_partition(to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM'));
SELECT ensure_raw_partition(to_char((now() AT TIME ZONE 'UTC') + interval '1 month', 'YYYY-MM'));

CREATE TABLE schema_baseline (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    resource_type text NOT NULL,
    version integer NOT NULL,
    fingerprint text NOT NULL,
    fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    sample_raw_id bigint,
    status text NOT NULL CHECK (status IN ('active', 'superseded')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_schema_baseline_active ON schema_baseline (tenant_id, platform_id, resource_type) WHERE status = 'active';

CREATE TABLE schema_drift_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL,
    baseline_id bigint NOT NULL,
    kind text NOT NULL CHECK (kind IN ('field_added', 'field_removed', 'type_changed')),
    path text NOT NULL,
    old_type text NOT NULL DEFAULT '',
    new_type text NOT NULL DEFAULT '',
    severity text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    sample_raw_id bigint,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'acked', 'resolved', 'ignored')),
    resolution text NOT NULL DEFAULT '',
    detected_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by bigint
);
CREATE INDEX idx_drift_open ON schema_drift_event (tenant_id, status, detected_at DESC);

CREATE TABLE mapping_profile (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    resource_type text NOT NULL,
    name text NOT NULL,
    active_version_id bigint,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_mapping_profile_platform FOREIGN KEY (platform_id) REFERENCES platform(id) ON DELETE RESTRICT,
    UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX uq_mapping_profile ON mapping_profile (tenant_id, platform_id, resource_type);
CREATE TABLE mapping_version (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    profile_id bigint NOT NULL,
    version_no integer NOT NULL CHECK (version_no > 0),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
    spec jsonb NOT NULL DEFAULT '{}'::jsonb,
    checksum text NOT NULL,
    note text NOT NULL DEFAULT '',
    published_by bigint,
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_mapping_version_profile FOREIGN KEY (tenant_id, profile_id) REFERENCES mapping_profile(tenant_id, id) ON DELETE RESTRICT,
    UNIQUE (tenant_id, profile_id, id)
);
CREATE UNIQUE INDEX uq_mapping_version ON mapping_version (profile_id, version_no);
ALTER TABLE mapping_profile ADD CONSTRAINT fk_mapping_profile_active_version
    FOREIGN KEY (tenant_id, id, active_version_id) REFERENCES mapping_version(tenant_id, profile_id, id) ON DELETE RESTRICT;

CREATE TABLE quarantine_record (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    resource_type text NOT NULL,
    external_id text NOT NULL,
    raw_id bigint NOT NULL,
    reason_code text NOT NULL CHECK (reason_code IN ('missing_required', 'type_mismatch', 'invalid_value', 'mapping_error')),
    reason_detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'replayed', 'discarded')),
    replayed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_quarantine_open ON quarantine_record (tenant_id, status, created_at DESC);
CREATE UNIQUE INDEX uq_quarantine_raw ON quarantine_record (raw_id, reason_code);

-- Schema only; catalog persistence is delivered in the next batch.
CREATE TABLE listing (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    platform_id bigint NOT NULL,
    spu_id bigint,
    external_product_id text NOT NULL,
    title text NOT NULL,
    price_cents bigint NOT NULL,
    currency char(3) NOT NULL,
    status text NOT NULL CHECK (status IN ('online', 'offline', 'deleted')),
    stock_total integer NOT NULL DEFAULT 0,
    raw_id bigint,
    synced_at timestamptz NOT NULL DEFAULT now(),
    ext jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_listing_external ON listing (tenant_id, shop_id, external_product_id);
CREATE INDEX idx_listing_spu ON listing (tenant_id, spu_id);
CREATE INDEX idx_listing_shop_status ON listing (tenant_id, shop_id, status, synced_at DESC);
CREATE TABLE listing_sku (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    listing_id bigint NOT NULL,
    sku_id bigint,
    external_sku_id text NOT NULL,
    title text NOT NULL,
    price_cents bigint NOT NULL,
    stock integer NOT NULL DEFAULT 0,
    ext jsonb NOT NULL DEFAULT '{}'::jsonb,
    synced_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_listing_sku ON listing_sku (listing_id, external_sku_id);

COMMIT;
