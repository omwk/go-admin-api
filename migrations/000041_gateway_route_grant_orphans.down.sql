BEGIN;
ALTER TABLE api_client_route ADD CONSTRAINT api_client_route_tenant_id_route_id_fkey
    FOREIGN KEY (tenant_id,route_id) REFERENCES api_route(tenant_id,id) ON DELETE RESTRICT NOT VALID;
COMMIT;
