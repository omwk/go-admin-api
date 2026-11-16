BEGIN;
ALTER TABLE audit_log
    ADD COLUMN actor_name varchar(128),
    ADD COLUMN actor_tenant_name varchar(128),
    ADD COLUMN resource_label varchar(128),
    ADD COLUMN error_code integer,
    ADD COLUMN error_message text;

-- Only this migration may rewrite historical rows; the trigger is restored atomically.
ALTER TABLE audit_log DISABLE TRIGGER audit_log_append_only;
UPDATE audit_log SET action = translate(action, ':_-', '...');
UPDATE audit_log a SET actor_name=left(u.username,128), actor_tenant_name=left(t.code,128)
FROM sys_user u JOIN tenant t ON t.id=u.tenant_id
WHERE a.actor_type='user' AND a.actor_id=u.id AND a.tenant_id=u.tenant_id;
UPDATE audit_log a SET actor_name=left(c.name,128)
FROM api_client c WHERE a.actor_type='api_client' AND a.actor_id=c.id AND a.tenant_id=c.tenant_id;
UPDATE audit_log a SET actor_tenant_name=left(t.code,128)
FROM tenant t WHERE a.tenant_id=t.id AND a.actor_tenant_name IS NULL;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM tenant r WHERE a.resource_type='tenant' AND a.resource_id=r.id::text;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM tenant_template r WHERE a.resource_type='tenant_template' AND a.resource_id=r.id::text;
UPDATE audit_log a SET resource_label=left(r.username,128)
FROM sys_user r WHERE a.resource_type='user' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM sys_dept r WHERE a.resource_type='dept' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM sys_role r WHERE a.resource_type='role' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM sys_menu r WHERE a.resource_type='menu' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM sys_permission r WHERE a.resource_type='permission' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.type,128)
FROM sys_dict r WHERE a.resource_type='dict' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.label,128)
FROM sys_dict_item r WHERE a.resource_type='dict_item' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM shop r WHERE a.resource_type='shop' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM customer r WHERE a.resource_type='customer' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM price_list r WHERE a.resource_type IN ('price_list','pricelist') AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.title,128)
FROM share_link r WHERE a.resource_type='share_link' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM api_client r WHERE a.resource_type='api_client' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM api_route r WHERE a.resource_type='api_route' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM pricing_plan r WHERE a.resource_type='pricing_plan' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.title,128)
FROM product_spu r WHERE a.resource_type='product' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log a SET resource_label=left(r.name,128)
FROM category r WHERE a.resource_type='category' AND a.resource_id=r.id::text AND a.tenant_id=r.tenant_id;
UPDATE audit_log SET resource_label=left(resource_id,128) WHERE resource_type='setting';
ALTER TABLE audit_log ENABLE TRIGGER audit_log_append_only;

-- The explicit maintenance command supplies a transaction-local, strict cutoff.
-- Existing three-year share retention remains available without that capability.
CREATE OR REPLACE FUNCTION restrict_share_audit_retention() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    prune_before timestamptz := NULLIF(current_setting('commercehub.audit_prune_before',true),'')::timestamptz;
BEGIN
    IF prune_before IS NOT NULL AND OLD.created_at < prune_before THEN
        RETURN OLD;
    END IF;
    IF NOT COALESCE(share_audit_retention_due(OLD.action,OLD.resource_type,OLD.created_at),false) THEN
        RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = '55000';
    END IF;
    RETURN OLD;
END;
$$;
COMMIT;
