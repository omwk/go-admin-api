BEGIN;
CREATE TABLE sys_dict (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1 REFERENCES tenant(id),
    type text NOT NULL CHECK (type ~ '^[a-z][a-z0-9_]{0,63}$'),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 256),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    builtin boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_sys_dict_tenant_type UNIQUE (tenant_id,type),
    CONSTRAINT uq_sys_dict_tenant_id UNIQUE (tenant_id,id)
);
CREATE TABLE sys_dict_item (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    dict_id bigint NOT NULL,
    label text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 256),
    value text NOT NULL CHECK (char_length(value) BETWEEN 1 AND 128),
    sort integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    color text NOT NULL DEFAULT '' CHECK (char_length(color)<=64),
    extra jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(extra)='object'),
    builtin boolean NOT NULL DEFAULT false,
    CONSTRAINT fk_sys_dict_item_tenant_dict FOREIGN KEY (tenant_id,dict_id) REFERENCES sys_dict(tenant_id,id),
    CONSTRAINT uq_sys_dict_item_tenant_dict_value UNIQUE (tenant_id,dict_id,value)
);
CREATE INDEX idx_sys_dict_item_tenant_dict_sort ON sys_dict_item(tenant_id,dict_id,sort,id);
COMMIT;
