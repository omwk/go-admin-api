-- M9c approved A: same durable admission writes, one application roundtrip.
BEGIN;
CREATE FUNCTION gateway_admit_call(payload jsonb, freeze_hash text) RETURNS jsonb
LANGUAGE plpgsql SECURITY INVOKER AS $$
DECLARE
    c api_call%ROWTYPE;
    h call_charge%ROWTYPE;
    quota_row api_quota%ROWTYPE;
    e entitlement%ROWTYPE;
    w wallet%ROWTYPE;
    changed integer;
    daily_limit bigint;
    monthly_limit bigint;
    quota_limit bigint;
    quota_kind text;
    day_start date;
    month_start date;
    quota_keys text[];
    reset_times timestamptz[];
    denied jsonb;
    pre bigint;
    amount bigint;
    i integer;
    reserved_at timestamptz;
BEGIN
    c := jsonb_populate_record(NULL::api_call,payload);
    c.result := coalesce(c.result,'null'::jsonb);
    reserved_at := (payload->>'reserve_at')::timestamptz;
    INSERT INTO api_call SELECT c.* ON CONFLICT DO NOTHING;
    GET DIAGNOSTICS changed = ROW_COUNT;
    IF changed=0 THEN RETURN jsonb_build_object('inserted',false,'code',0); END IF;
    INSERT INTO api_call_log(call_id,tenant_id,client_id,route_id,route_version,trace_id,request_id,method,path,ip_hash,called_at,state,admitted,status_code,latency_ms,req_bytes,resp_bytes,billing_mode)
    VALUES(c.call_id,c.tenant_id,c.client_id,c.route_id,c.route_version,c.trace_id,c.request_id,c.method,c.path,c.ip_hash,c.called_at,'processing',c.admitted,0,0,0,0,c.billing_mode);
    IF NOT c.admitted THEN RETURN jsonb_build_object('inserted',true,'code',0); END IF;
    IF c.billing_mode='per_call' THEN
        IF c.currency<>'CNY' OR c.price_cents<=0 OR c.charge_units<=0
          OR c.price_cents::numeric*c.charge_units>9223372036854775807 THEN
            RETURN jsonb_build_object('inserted',true,'code',10001);
        END IF;
        pre := c.price_cents*c.charge_units;
        INSERT INTO call_charge(call_id,tenant_id,wallet_id,app_id,currency,route_id,route_version,price_cents,units,pre_cents,state,actual_cents,created_at,deadline_at,source)
        VALUES(c.call_id,c.tenant_id,0,c.app_id,c.currency,c.route_id,c.route_version,c.price_cents,c.charge_units,pre,'reserved',0,c.called_at,c.deadline_at,'pending') ON CONFLICT DO NOTHING;
        SELECT * INTO h FROM call_charge WHERE call_id=c.call_id AND tenant_id=c.tenant_id FOR UPDATE;
        IF NOT FOUND OR h.app_id<>c.app_id OR h.currency<>c.currency OR h.route_id<>c.route_id
          OR h.route_version<>c.route_version OR h.price_cents<>c.price_cents OR h.units<>c.charge_units
          OR h.created_at<>c.called_at OR h.deadline_at<>c.deadline_at THEN
            RETURN jsonb_build_object('inserted',true,'code',10006);
        END IF;
    END IF;
    SELECT quota_daily,quota_monthly INTO daily_limit,monthly_limit FROM api_client_route
    WHERE tenant_id=c.tenant_id AND client_id=c.client_id AND route_id=c.route_id
      AND status='active' AND (expires_at IS NULL OR expires_at>c.called_at) FOR SHARE;
    IF NOT FOUND THEN RETURN jsonb_build_object('inserted',true,'code',80104); END IF;
    day_start := (c.called_at AT TIME ZONE 'UTC')::date;
    month_start := date_trunc('month',c.called_at AT TIME ZONE 'UTC')::date;
    quota_keys := ARRAY['quota:'||c.client_id||':'||to_char(day_start,'YYYY-MM-DD'),'quota:'||c.client_id||':'||to_char(month_start,'YYYY-MM')];
    reset_times := ARRAY[(day_start+1)::timestamp AT TIME ZONE 'UTC',(month_start+interval '1 month')::timestamp AT TIME ZONE 'UTC'];
    FOR i IN 1..2 LOOP
        INSERT INTO api_quota(tenant_id,client_id,quota_key,admitted_count,reset_at)
        VALUES(c.tenant_id,c.client_id,quota_keys[i],0,reset_times[i]) ON CONFLICT DO NOTHING;
        SELECT * INTO STRICT quota_row FROM api_quota WHERE tenant_id=c.tenant_id AND client_id=c.client_id AND quota_key=quota_keys[i] FOR UPDATE;
        quota_limit := CASE WHEN i=1 THEN daily_limit ELSE monthly_limit END;
        quota_kind := CASE WHEN i=1 THEN 'daily_quota' ELSE 'monthly_quota' END;
        IF quota_limit>0 AND quota_row.admitted_count>=quota_limit THEN
            denied := jsonb_build_object('inserted',true,'code',10005,'kind',quota_kind,'reset_at',quota_row.reset_at,'limit',quota_limit);
        END IF;
    END LOOP;
    IF denied IS NOT NULL THEN RETURN denied; END IF;
    UPDATE api_quota SET admitted_count=admitted_count+1
    WHERE tenant_id=c.tenant_id AND client_id=c.client_id AND quota_key=ANY(quota_keys);
    IF c.billing_mode<>'per_call' OR h.state<>'reserved' THEN
        RETURN jsonb_build_object('inserted',true,'code',0);
    END IF;
    -- Existing reservations are never frozen again.
    IF h.source<>'pending' THEN RETURN jsonb_build_object('inserted',true,'code',0); END IF;
    SELECT * INTO e FROM entitlement WHERE tenant_id=c.tenant_id AND app_id=c.app_id AND currency=c.currency
      AND status='active' AND starts_at<=reserved_at AND (ends_at IS NULL OR ends_at>reserved_at)
      AND route_id IN (0,c.route_id) AND remaining_units-reserved_units>=CASE WHEN quota_type='calls' THEN 1 ELSE c.charge_units END
    ORDER BY CASE kind WHEN 'free' THEN 0 ELSE 1 END,ends_at ASC NULLS LAST,id LIMIT 1 FOR UPDATE;
    IF FOUND THEN
        amount := CASE WHEN e.quota_type='calls' THEN 1 ELSE c.charge_units END;
        UPDATE entitlement SET reserved_units=reserved_units+amount,version=version+1
        WHERE id=e.id AND tenant_id=e.tenant_id AND version=e.version AND remaining_units-reserved_units>=amount RETURNING * INTO e;
        IF NOT FOUND THEN RETURN jsonb_build_object('inserted',true,'code',90104); END IF;
        INSERT INTO unit_txn(tenant_id,app_id,currency,entitlement_id,call_id,type,amount,remaining_delta,reserved_delta,remaining_after,reserved_after,version_after,idempotency_key,occurred_at,service_date)
        VALUES(e.tenant_id,e.app_id,e.currency,e.id,c.call_id,'reserve',amount,0,amount,e.remaining_units,e.reserved_units,e.version,'billing:unit:'||e.id||':'||e.version,reserved_at,day_start);
        UPDATE call_charge SET source=e.kind,entitlement_id=e.id,reserved_units=amount
        WHERE call_id=c.call_id AND tenant_id=c.tenant_id AND source='pending' AND state='reserved';
    ELSE
        SELECT * INTO w FROM wallet WHERE tenant_id=c.tenant_id AND app_id=c.app_id AND currency=c.currency;
        IF NOT FOUND THEN RETURN jsonb_build_object('inserted',true,'code',90101); END IF;
        UPDATE call_charge SET source='wallet',wallet_id=w.id
        WHERE call_id=c.call_id AND tenant_id=c.tenant_id AND source='pending' AND state='reserved';
        UPDATE wallet SET frozen_cents=frozen_cents+pre,version=version+1,updated_at=reserved_at
        WHERE id=w.id AND tenant_id=c.tenant_id AND currency=c.currency AND status='active'
          AND balance_cents-frozen_cents>=pre RETURNING * INTO w;
        IF NOT FOUND THEN
            IF EXISTS (SELECT 1 FROM wallet WHERE tenant_id=c.tenant_id AND app_id=c.app_id AND currency=c.currency AND status<>'active') THEN RETURN jsonb_build_object('inserted',true,'code',90103); END IF;
            RETURN jsonb_build_object('inserted',true,'code',90102);
        END IF;
        INSERT INTO wallet_txn(tenant_id,wallet_id,type,amount_cents,balance_delta_cents,frozen_delta_cents,balance_after_cents,frozen_after_cents,version_after,call_id,idempotency_key,request_hash,actor_id,note,occurred_at,biz_type,biz_id,service_date)
        VALUES(c.tenant_id,w.id,'freeze',pre,0,pre,w.balance_cents,w.frozen_cents,w.version,c.call_id,'billing:call:'||c.tenant_id||':'||c.call_id||':freeze',freeze_hash,0,'',reserved_at,'api_call','',day_start);
    END IF;
    RETURN jsonb_build_object('inserted',true,'code',0);
END;
$$;
COMMIT;
