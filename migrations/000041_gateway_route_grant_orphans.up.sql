BEGIN;
ALTER TABLE api_client_route DROP CONSTRAINT api_client_route_tenant_id_route_id_fkey;
COMMIT;
