BEGIN;
CREATE TABLE tenant_template (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    roles jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(roles) = 'array'),
    menus jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(menus) = 'array'),
    permissions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(permissions) = 'array'),
    settings jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(settings) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_tenant_template_code ON tenant_template (code);
ALTER TABLE tenant ADD COLUMN provisioned_from bigint REFERENCES tenant_template(id) ON DELETE RESTRICT;
COMMIT;
