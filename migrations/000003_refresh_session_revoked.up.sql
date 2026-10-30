CREATE INDEX CONCURRENTLY idx_refresh_session_revoked
    ON sys_refresh_token (tenant_id, session_id, user_id)
    WHERE revoked_reason IN ('logout', 'kick', 'replay');
