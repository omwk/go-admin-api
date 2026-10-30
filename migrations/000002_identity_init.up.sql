BEGIN;

CREATE TABLE sys_dept (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    parent_id bigint NOT NULL DEFAULT 0,
    name text NOT NULL,
    path text NOT NULL,
    sort integer NOT NULL DEFAULT 0,
    status text NOT NULL
);
CREATE UNIQUE INDEX uq_sys_dept_tenant_id ON sys_dept (tenant_id, id);
CREATE INDEX idx_sys_dept_path ON sys_dept (tenant_id, path);

CREATE TABLE sys_user (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    username text NOT NULL,
    password_hash text NOT NULL,
    nickname text,
    email text,
    phone text,
    avatar text,
    dept_id bigint NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'disabled', 'locked')),
    failed_attempts integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    last_login_at timestamptz,
    last_login_ip inet,
    is_super boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT fk_sys_user_dept FOREIGN KEY (tenant_id, dept_id)
        REFERENCES sys_dept (tenant_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX uq_sys_user_tenant_id ON sys_user (tenant_id, id);
CREATE UNIQUE INDEX uq_sys_user_username ON sys_user (tenant_id, username) WHERE deleted_at IS NULL;
CREATE INDEX idx_sys_user_dept ON sys_user (tenant_id, dept_id);

CREATE TABLE sys_role (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    code text NOT NULL,
    name text NOT NULL,
    data_scope text NOT NULL CHECK (data_scope IN ('all', 'dept', 'dept_and_child', 'self', 'custom')),
    remark text,
    status text NOT NULL
);
CREATE UNIQUE INDEX uq_sys_role_tenant_id ON sys_role (tenant_id, id);
CREATE UNIQUE INDEX uq_sys_role_code ON sys_role (tenant_id, code);

CREATE TABLE sys_permission (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    code text NOT NULL,
    name text NOT NULL,
    module text NOT NULL CHECK (module IN ('system', 'shop', 'product', 'trade', 'crm', 'share', 'sync', 'gateway', 'billing')),
    type text NOT NULL CHECK (type IN ('api', 'button', 'data'))
);
CREATE UNIQUE INDEX uq_sys_permission_tenant_id ON sys_permission (tenant_id, id);
CREATE UNIQUE INDEX uq_sys_permission_code ON sys_permission (tenant_id, code);

CREATE TABLE sys_menu (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    parent_id bigint NOT NULL DEFAULT 0,
    name text NOT NULL,
    type text NOT NULL CHECK (type IN ('dir', 'menu', 'button', 'link')),
    path text,
    component text,
    redirect text,
    icon text,
    sort integer NOT NULL DEFAULT 0,
    visible boolean NOT NULL DEFAULT false,
    keep_alive boolean NOT NULL DEFAULT false,
    is_external boolean NOT NULL DEFAULT false,
    permission_code text,
    meta jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL CHECK (status IN ('active', 'disabled')),
    CONSTRAINT fk_sys_menu_permission FOREIGN KEY (tenant_id, permission_code)
        REFERENCES sys_permission (tenant_id, code) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX uq_sys_menu_tenant_id ON sys_menu (tenant_id, id);
CREATE INDEX idx_sys_menu_parent ON sys_menu (tenant_id, parent_id, sort);

CREATE TABLE sys_user_role (
    tenant_id bigint NOT NULL DEFAULT 1,
    user_id bigint NOT NULL,
    role_id bigint NOT NULL,
    PRIMARY KEY (user_id, role_id),
    CONSTRAINT fk_sys_user_role_user FOREIGN KEY (tenant_id, user_id)
        REFERENCES sys_user (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_sys_user_role_role FOREIGN KEY (tenant_id, role_id)
        REFERENCES sys_role (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX idx_sys_user_role_tenant_role ON sys_user_role (tenant_id, role_id);

CREATE TABLE sys_role_menu (
    tenant_id bigint NOT NULL DEFAULT 1,
    role_id bigint NOT NULL,
    menu_id bigint NOT NULL,
    PRIMARY KEY (role_id, menu_id),
    CONSTRAINT fk_sys_role_menu_role FOREIGN KEY (tenant_id, role_id)
        REFERENCES sys_role (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_sys_role_menu_menu FOREIGN KEY (tenant_id, menu_id)
        REFERENCES sys_menu (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX idx_sys_role_menu_tenant_menu ON sys_role_menu (tenant_id, menu_id);

CREATE TABLE sys_role_permission (
    tenant_id bigint NOT NULL DEFAULT 1,
    role_id bigint NOT NULL,
    permission_id bigint NOT NULL,
    PRIMARY KEY (role_id, permission_id),
    CONSTRAINT fk_sys_role_permission_role FOREIGN KEY (tenant_id, role_id)
        REFERENCES sys_role (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_sys_role_permission_permission FOREIGN KEY (tenant_id, permission_id)
        REFERENCES sys_permission (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX idx_sys_role_permission_tenant_permission ON sys_role_permission (tenant_id, permission_id);

CREATE TABLE sys_role_dept (
    tenant_id bigint NOT NULL DEFAULT 1,
    role_id bigint NOT NULL,
    dept_id bigint NOT NULL,
    PRIMARY KEY (role_id, dept_id),
    CONSTRAINT fk_sys_role_dept_role FOREIGN KEY (tenant_id, role_id)
        REFERENCES sys_role (tenant_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_sys_role_dept_dept FOREIGN KEY (tenant_id, dept_id)
        REFERENCES sys_dept (tenant_id, id) ON DELETE RESTRICT
);
CREATE INDEX idx_sys_role_dept_tenant_dept ON sys_role_dept (tenant_id, dept_id);

CREATE TABLE sys_refresh_token (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL DEFAULT 1,
    user_id bigint NOT NULL,
    session_id text NOT NULL,
    token_hash text NOT NULL,
    replaced_by_token_id bigint,
    revoked_reason text CHECK (revoked_reason IN ('rotated', 'logout', 'kick', 'replay', 'expired')),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    ip inet,
    user_agent text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_refresh_token_hash ON sys_refresh_token (token_hash);
CREATE INDEX idx_refresh_user ON sys_refresh_token (tenant_id, user_id, expires_at DESC);
CREATE INDEX idx_refresh_session ON sys_refresh_token (tenant_id, session_id) WHERE revoked_at IS NULL;

COMMIT;
