BEGIN;
CREATE TABLE notification_preference (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id bigint NOT NULL REFERENCES tenant(id),
 user_id bigint NOT NULL,
 template text NOT NULL,
 channel text NOT NULL CHECK(channel IN ('email','sms','wecom','dingtalk','webhook')),
 enabled boolean NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,user_id,template,channel),
 FOREIGN KEY(tenant_id,user_id) REFERENCES sys_user(tenant_id,id)
);
COMMIT;
