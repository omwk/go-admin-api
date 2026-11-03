BEGIN;

-- 强制装到 public：迁移在隔离 schema 里执行时，扩展若装进测试 schema，
-- 该 schema 一被 DROP CASCADE 就会留下孤儿扩展记录。
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE category (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    parent_id bigint NOT NULL DEFAULT 0,
    name text NOT NULL,
    path text NOT NULL,
    level integer NOT NULL CHECK (level > 0),
    sort integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_category_path ON category (tenant_id, path);

CREATE TABLE platform_category (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    external_id text NOT NULL,
    parent_external_id text NOT NULL DEFAULT '',
    name text NOT NULL,
    path text NOT NULL,
    is_leaf boolean NOT NULL DEFAULT true,
    synced_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_platform_category UNIQUE (tenant_id, platform_id, external_id)
);

CREATE TABLE category_mapping (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    platform_id bigint NOT NULL,
    platform_category_id bigint NOT NULL,
    category_id bigint,
    confidence numeric(3,2) NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    source text NOT NULL DEFAULT 'default' CHECK (source IN ('manual', 'rule', 'default')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_category_mapping UNIQUE (tenant_id, platform_category_id)
);

CREATE TABLE product_spu (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    code text,
    title text NOT NULL,
    subtitle text NOT NULL DEFAULT '',
    brand text NOT NULL DEFAULT '',
    category_id bigint,
    main_image text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived')),
    source text NOT NULL CHECK (source IN ('platform', 'manual')),
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    ext jsonb NOT NULL DEFAULT '{}'::jsonb,
    price_min_cents bigint NOT NULL DEFAULT 0,
    price_max_cents bigint NOT NULL DEFAULT 0,
    sku_count integer NOT NULL DEFAULT 0 CHECK (sku_count >= 0),
    version integer NOT NULL DEFAULT 0 CHECK (version >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX uq_product_spu_code ON product_spu (tenant_id, code) WHERE code IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX idx_product_spu_list ON product_spu (tenant_id, status, updated_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_product_spu_title_trgm ON product_spu USING gin (title public.gin_trgm_ops);

CREATE TABLE product_sku (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    spu_id bigint NOT NULL,
    code text,
    barcode text NOT NULL DEFAULT '',
    title text NOT NULL,
    price_cents bigint NOT NULL DEFAULT 0,
    cost_cents bigint NOT NULL DEFAULT 0,
    currency char(3) NOT NULL,
    stock_cached integer NOT NULL DEFAULT 0,
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX uq_product_sku_code ON product_sku (tenant_id, spu_id, code) WHERE code IS NOT NULL;
CREATE INDEX idx_product_sku_spu ON product_sku (tenant_id, spu_id);

CREATE TABLE product_media (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    spu_id bigint NOT NULL,
    sku_id bigint,
    type text NOT NULL CHECK (type IN ('image', 'video', 'detail')),
    url text NOT NULL,
    sort integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_media_spu ON product_media (tenant_id, spu_id, sort);

CREATE TABLE inventory_snapshot (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    shop_id bigint NOT NULL,
    listing_sku_id bigint NOT NULL,
    available integer NOT NULL,
    reserved integer NOT NULL DEFAULT 0,
    synced_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_inv_snapshot ON inventory_snapshot (listing_sku_id, synced_at DESC);

CREATE TABLE export_job (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    kind text NOT NULL CHECK (kind IN ('products', 'orders')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'expired')),
    filters jsonb NOT NULL DEFAULT '{}'::jsonb,
    requested_by bigint NOT NULL,
    file_id bigint,
    row_count bigint NOT NULL DEFAULT 0,
    error_msg text NOT NULL DEFAULT '',
    object_key text NOT NULL,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz
);
CREATE INDEX idx_export_job ON export_job (tenant_id, status, created_at DESC);

COMMIT;
