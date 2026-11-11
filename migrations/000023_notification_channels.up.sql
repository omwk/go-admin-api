BEGIN;
CREATE TABLE channel_provider (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 tenant_id bigint REFERENCES tenant(id),
 channel text NOT NULL CHECK(channel IN ('email','sms','wecom','dingtalk','webhook')),
 kind text NOT NULL CHECK((channel='email' AND kind IN ('smtp','aliyun_dm','sendgrid')) OR (channel='sms' AND kind='aliyun_sms') OR (channel='wecom' AND kind='wecom_bot') OR (channel='dingtalk' AND kind='dingtalk_bot') OR (channel='webhook' AND kind='http')),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 config jsonb NOT NULL CHECK(jsonb_typeof(config)='object'),
 enabled boolean NOT NULL DEFAULT false, priority integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_channel_provider_tenant ON channel_provider(tenant_id,channel,name) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX uq_channel_provider_global ON channel_provider(channel,name) WHERE tenant_id IS NULL;
CREATE INDEX idx_channel_provider_selection ON channel_provider(channel,priority,id) WHERE enabled;
CREATE TABLE notify_template (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, tenant_id bigint REFERENCES tenant(id),
 code text NOT NULL, locale text NOT NULL DEFAULT 'zh-CN', subject text NOT NULL, body text NOT NULL,
 format text NOT NULL CHECK(format IN ('text','html')), enabled boolean NOT NULL DEFAULT true
);
CREATE UNIQUE INDEX uq_notify_template_tenant ON notify_template(tenant_id,code,locale) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX uq_notify_template_global ON notify_template(code,locale) WHERE tenant_id IS NULL;
CREATE TABLE notify_delivery (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, tenant_id bigint NOT NULL,
 channel text NOT NULL CHECK(channel IN ('email','sms','wecom','dingtalk','webhook')), template_code text NOT NULL,
 recipient_hash text NOT NULL DEFAULT '' CHECK(recipient_hash='' OR recipient_hash ~ '^[0-9a-f]{64}$'), recipient_domain text NOT NULL DEFAULT '',
 provider_id bigint, status text NOT NULL CHECK(status IN ('pending','sent','failed','skipped')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0), last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), sent_at timestamptz, next_retry_at timestamptz
);
CREATE INDEX idx_notify_delivery_page ON notify_delivery(tenant_id,created_at DESC,id DESC);
CREATE TABLE notify_outbox (
 id bigint PRIMARY KEY, tenant_id bigint NOT NULL, channel text NOT NULL, template_code text NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 status text NOT NULL CHECK(status IN ('pending','processing','channel_unavailable','sent','failed'))
);
CREATE INDEX idx_notify_outbox_pending ON notify_outbox(id) WHERE status IN ('pending','processing','channel_unavailable');
INSERT INTO notify_template(code,locale,subject,body,format) VALUES
 ('notification.test','zh-CN','通道测试','通知通道测试','text'),
 ('notification.default','zh-CN','系统通知','{{.Name}}，您有一条系统通知。','text');
INSERT INTO notify_template(code,locale,subject,body,format)
 SELECT code,'zh-CN','凭据维护通知','{{.Platform}}: {{.Severity}} — {{.Path}}','text'
 FROM unnest(ARRAY['credential.expiring.3d','credential.expiring.1d','credential.expired','credential.needs_reauth','credential.unknown','credential.failed','credential.stuck','credential.recovered','credential.protocol_pending']) AS code;
COMMIT;
