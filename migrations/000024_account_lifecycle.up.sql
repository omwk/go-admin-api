BEGIN;
-- phone/email and the partial username index already exist in identity_init.
ALTER TABLE sys_user
 ADD COLUMN password_changed_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN force_password_change boolean NOT NULL DEFAULT false,
 ADD COLUMN totp_secret text;
CREATE UNIQUE INDEX uq_sys_user_phone ON sys_user(tenant_id,phone) WHERE deleted_at IS NULL AND phone IS NOT NULL;
CREATE UNIQUE INDEX uq_sys_user_email ON sys_user(tenant_id,lower(email)) WHERE deleted_at IS NULL AND email IS NOT NULL;

CREATE TABLE password_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id bigint NOT NULL,
 user_id bigint NOT NULL,
 password_hash text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(tenant_id,user_id) REFERENCES sys_user(tenant_id,id)
);
CREATE INDEX idx_password_history_user ON password_history(tenant_id,user_id,id DESC);

CREATE TABLE auth_token (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id bigint NOT NULL REFERENCES tenant(id),
 user_id bigint,
 purpose text NOT NULL CHECK(purpose IN ('invite','reset','verify')),
 token_hash text NOT NULL CHECK(token_hash ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL,
 used_at timestamptz,
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 created_by bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 dept_id bigint,
 role_ids jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(role_ids)='array'),
 username text,
 max_uses integer NOT NULL DEFAULT 1 CHECK(max_uses>=0),
 used_count integer NOT NULL DEFAULT 0 CHECK(used_count>=0 AND (max_uses=0 OR used_count<=max_uses)),
 revoked_at timestamptz,
 FOREIGN KEY(tenant_id,user_id) REFERENCES sys_user(tenant_id,id),
 FOREIGN KEY(tenant_id,created_by) REFERENCES sys_user(tenant_id,id),
 FOREIGN KEY(tenant_id,dept_id) REFERENCES sys_dept(tenant_id,id),
 CHECK(purpose='invite' OR (user_id IS NOT NULL AND max_uses=1)),
 CHECK(purpose<>'invite' OR dept_id IS NOT NULL)
);
CREATE UNIQUE INDEX uq_auth_token_hash ON auth_token(token_hash);
CREATE INDEX idx_auth_token_page ON auth_token(tenant_id,purpose,created_at DESC,id DESC);
CREATE INDEX idx_auth_token_user ON auth_token(tenant_id,user_id,purpose);
INSERT INTO notify_template(code,locale,subject,body,format) VALUES
 ('identity.invite','zh-CN','账号邀请','请打开邀请链接：{{.Link}}','text'),
 ('identity.reset','zh-CN','重置密码','请打开重置链接：{{.Link}}','text');
COMMIT;
