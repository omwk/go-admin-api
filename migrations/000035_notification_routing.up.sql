BEGIN;
CREATE TABLE sys_user_channel (
 id bigserial PRIMARY KEY,
 tenant_id bigint NOT NULL REFERENCES tenant(id),
 user_id bigint NOT NULL REFERENCES sys_user(id),
 channel text NOT NULL CHECK(channel IN ('webhook','bark','gotify')),
 endpoint_enc bytea NOT NULL,
 verified_at timestamptz,
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,user_id,channel),
 FOREIGN KEY(tenant_id,user_id) REFERENCES sys_user(tenant_id,id)
);
ALTER TABLE notify_delivery DROP CONSTRAINT notify_delivery_channel_check;
ALTER TABLE notify_delivery ADD CONSTRAINT notify_delivery_channel_check CHECK(channel IN ('email','sms','wecom','dingtalk','webhook','bark','gotify'));
ALTER TABLE notification_preference DROP CONSTRAINT notification_preference_channel_check;
ALTER TABLE notification_preference ADD CONSTRAINT notification_preference_channel_check CHECK(channel IN ('email','sms','wecom','dingtalk','webhook','bark','gotify'));
COMMIT;
