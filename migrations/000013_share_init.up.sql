-- M6a / ADR-020: business references are tenant-checked in application transactions.
BEGIN;
CREATE TABLE customer (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    code text NOT NULL CHECK (length(code) BETWEEN 1 AND 100),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    contact_name_cipher bytea NOT NULL,
    contact_name_nonce bytea NOT NULL CHECK (octet_length(contact_name_nonce)=12),
    phone_cipher bytea NOT NULL,
    phone_nonce bytea NOT NULL CHECK (octet_length(phone_nonce)=12),
    email_cipher bytea NOT NULL,
    email_nonce bytea NOT NULL CHECK (octet_length(email_nonce)=12),
    key_version integer NOT NULL CHECK (key_version>0),
    version integer NOT NULL DEFAULT 1 CHECK (version>0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    owner_user_id bigint NOT NULL CHECK (owner_user_id>0),
    source text NOT NULL DEFAULT 'manual' CHECK (length(source) BETWEEN 1 AND 100),
    level text NOT NULL DEFAULT '' CHECK (level IN ('','A','B','C')),
    tags jsonb NOT NULL DEFAULT '[]'::jsonb,
    ext jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT uq_customer_code UNIQUE (tenant_id,code),
    CHECK (octet_length(contact_name_cipher)>=16 AND octet_length(phone_cipher)>=16 AND octet_length(email_cipher)>=16)
);
CREATE INDEX idx_customer_owner ON customer (tenant_id,owner_user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_customer_created ON customer (tenant_id,created_at DESC,id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_customer_name_trgm ON customer USING gin (name public.gin_trgm_ops) WHERE deleted_at IS NULL;

CREATE TABLE price_list (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    type text NOT NULL CHECK (type IN ('customer','default')),
    customer_id bigint,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    currency char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','expired','disabled')),
    valid_from timestamptz,
    valid_to timestamptz,
    note_cipher bytea NOT NULL CHECK (octet_length(note_cipher)>=16),
    note_nonce bytea NOT NULL CHECK (octet_length(note_nonce)=12),
    key_version integer NOT NULL CHECK (key_version>0),
    revision integer NOT NULL DEFAULT 1 CHECK (revision>0),
    version integer NOT NULL DEFAULT 1 CHECK (version>0),
    created_by bigint NOT NULL CHECK (created_by>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK ((type='customer' AND customer_id>0) OR (type='default' AND customer_id IS NULL)),
    CHECK (type<>'customer' OR customer_id IS NOT NULL),
    CHECK (valid_from IS NULL OR valid_to IS NULL OR valid_from<valid_to)
);
CREATE INDEX idx_price_list ON price_list (tenant_id,customer_id,status) WHERE deleted_at IS NULL;
CREATE INDEX idx_price_list_creator ON price_list (tenant_id,created_by,created_at DESC,id DESC) WHERE deleted_at IS NULL;

CREATE TABLE price_list_item (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    price_list_id bigint NOT NULL CHECK (price_list_id>0),
    spu_id bigint NOT NULL CHECK (spu_id>0),
    sku_id bigint NOT NULL DEFAULT 0 CHECK (sku_id>=0),
    price_cents bigint NOT NULL CHECK (price_cents>=0),
    moq integer NOT NULL DEFAULT 1 CHECK (moq>0),
    note_cipher bytea NOT NULL CHECK (octet_length(note_cipher)>=16),
    note_nonce bytea NOT NULL CHECK (octet_length(note_nonce)=12),
    key_version integer NOT NULL CHECK (key_version>0),
    revision integer NOT NULL DEFAULT 1 CHECK (revision>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_price_item UNIQUE (tenant_id,price_list_id,spu_id,sku_id)
);

-- The remaining four tables have no service, token issuer or anonymous route in M6a.
CREATE TABLE share_link (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    token_prefix text NOT NULL CHECK (length(token_prefix)=8),
    type text NOT NULL CHECK (type IN ('products','price_list')),
    mode text NOT NULL DEFAULT 'snapshot' CHECK (mode IN ('snapshot','live')),
    price_list_id bigint,
    customer_id bigint,
    title text NOT NULL,
    password_hash text,
    expires_at timestamptz,
    max_views integer NOT NULL DEFAULT 0 CHECK (max_views>=0),
    view_count integer NOT NULL DEFAULT 0 CHECK (view_count>=0),
    allow_inquiry boolean NOT NULL DEFAULT true,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked','expired')),
    content_version integer NOT NULL DEFAULT 1 CHECK (content_version>0),
    created_by bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CHECK (customer_id IS NULL OR password_hash IS NOT NULL)
);
CREATE INDEX idx_share_link_creator ON share_link (tenant_id,created_by,created_at DESC);
CREATE TABLE share_snapshot (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    share_link_id bigint NOT NULL UNIQUE,
    payload jsonb NOT NULL,
    checksum text NOT NULL,
    item_count integer NOT NULL CHECK (item_count>=0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE share_visit (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    share_link_id bigint NOT NULL,
    result text NOT NULL,
    ip_hash text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_share_visit ON share_visit (share_link_id,created_at DESC);
CREATE TABLE inquiry (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL,
    share_link_id bigint NOT NULL,
    customer_id bigint,
    contact_name_cipher bytea NOT NULL CHECK (octet_length(contact_name_cipher)>=16),
    contact_name_nonce bytea NOT NULL CHECK (octet_length(contact_name_nonce)=12),
    contact_phone_cipher bytea NOT NULL CHECK (octet_length(contact_phone_cipher)>=16),
    contact_phone_nonce bytea NOT NULL CHECK (octet_length(contact_phone_nonce)=12),
    contact_email_cipher bytea NOT NULL CHECK (octet_length(contact_email_cipher)>=16),
    contact_email_nonce bytea NOT NULL CHECK (octet_length(contact_email_nonce)=12),
    company_cipher bytea NOT NULL CHECK (octet_length(company_cipher)>=16),
    company_nonce bytea NOT NULL CHECK (octet_length(company_nonce)=12),
    message_cipher bytea NOT NULL CHECK (octet_length(message_cipher)>=16),
    message_nonce bytea NOT NULL CHECK (octet_length(message_nonce)=12),
    items_cipher bytea NOT NULL CHECK (octet_length(items_cipher)>=16),
    items_nonce bytea NOT NULL CHECK (octet_length(items_nonce)=12),
    key_version integer NOT NULL CHECK (key_version>0),
    revision integer NOT NULL DEFAULT 1 CHECK (revision>0),
    status text NOT NULL DEFAULT 'new' CHECK (status IN ('new','contacted','quoted','won','lost')),
    owner_user_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_inquiry ON inquiry (tenant_id,status,created_at DESC);
COMMIT;
