package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/stretchr/testify/require"
)

func TestAuditSemanticsChangesAndNoop(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "semantics")
	event := Event{TenantID: 1, ActorType: "user", Action: "system:tenant:update", ResourceType: "tenant", ResourceID: "2", Result: "success",
		Before: map[string]any{"name": "old", "status": "active"}, After: map[string]any{"name": "new", "status": "active"}}
	executor := &fakeExecutor{}
	require.NoError(t, Record(ctx, executor, event))
	require.Equal(t, "system.tenant.update", executor.args[3])
	require.JSONEq(t, `{"name":"old"}`, string(executor.args[6].([]byte)))
	require.JSONEq(t, `{"name":"new"}`, string(executor.args[7].([]byte)))
	executor.query = ""
	event.Before = event.After
	require.NoError(t, Record(ctx, executor, event))
	require.Empty(t, executor.query)
}

func TestAuditSemanticsContextNamesAndNull(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "metadata")
	ctx = WithRequestMeta(ctx, RequestMeta{IP: "127.0.0.1", UserAgent: "audit-test"})
	executor := &fakeExecutor{}
	event := Event{TenantID: 1, ActorType: "user", Action: "system.tenant.create", ResourceType: "tenant", ResourceID: "2", Result: "success",
		ActorName: "admin", ActorTenantName: "default", ResourceLabel: strings.Repeat("名", 129), After: map[string]any{"name": "new"}}
	require.NoError(t, Record(ctx, executor, event))
	require.Equal(t, []byte("null"), executor.args[6])
	require.Equal(t, "127.0.0.1", executor.args[8])
	require.Equal(t, "audit-test", executor.args[9])
	require.Equal(t, "admin", executor.args[12])
	require.Equal(t, "default", executor.args[13])
	require.Equal(t, strings.Repeat("名", 128), executor.args[14])
}

func TestAuditSemanticsExplicitDeletionKeepsBeforeSnapshot(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "delete")
	executor := &fakeExecutor{}
	state := map[string]any{"name": "removed resource", "status": "active"}
	event := Event{TenantID: 1, ActorType: "user", Action: "system.dept.delete", ResourceType: "dept", ResourceID: "2", Result: "success",
		Before: state, After: state, AfterNull: true}
	require.NoError(t, Record(ctx, executor, event))
	require.NotEmpty(t, executor.query, "the explicit deletion marker must bypass update noop detection")
	require.JSONEq(t, `{"name":"removed resource","status":"active"}`, string(executor.args[6].([]byte)))
	require.JSONEq(t, "null", string(executor.args[7].([]byte)))
}

func TestAuditSemanticsRedactsBeforePersistenceAndKeepsSecretChanges(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "redaction")
	executor := &fakeExecutor{}
	event := Event{TenantID: 1, ActorType: "user", Action: "identity.password.reset", ResourceType: "user", ResourceID: "1", Result: "success",
		Before: map[string]any{"password_hash": "old"}, After: map[string]any{"password_hash": "new", "nested": []map[string]string{{"access_token": "private"}}}}
	require.NoError(t, Record(ctx, executor, event))
	require.NotEmpty(t, executor.query)
	require.JSONEq(t, `{"password_hash":"***","nested":null}`, string(executor.args[6].([]byte)))
	var after map[string]any
	require.NoError(t, json.Unmarshal(executor.args[7].([]byte), &after))
	require.Equal(t, "***", after["password_hash"])
	require.NotContains(t, string(executor.args[7].([]byte)), "private")
}

func TestAuditSemanticsFailureNeverPersistsRawCauseOrMessage(t *testing.T) {
	event := Event{TenantID: 1, ActorType: "user", Action: "identity.login", ResourceType: "user", ResourceID: "1"}
	Failure(&event, &errs.AppError{Code: errs.ErrConflict, Message: "untrusted cause", Cause: errors.New("private database error")})
	require.Equal(t, "failure", event.Result)
	require.Equal(t, errs.ErrConflict, *event.ErrorCode)
	require.NotEmpty(t, event.ErrorMessage)
	require.NotContains(t, event.ErrorMessage, "untrusted")
	require.NotContains(t, event.ErrorMessage, "private")
}

func TestAuditSemanticsFailureWithUnchangedStateIsStillRecorded(t *testing.T) {
	ctx := logger.WithTraceID(context.Background(), "failure")
	executor := &fakeExecutor{}
	event := Event{TenantID: 1, ActorType: "user", Action: "identity.login", ResourceType: "user", ResourceID: "1",
		Before: map[string]any{"status": "active"}, After: map[string]any{"status": "active"}}
	Failure(&event, errs.New(20101, 401, "untrusted text"))
	require.NoError(t, Record(ctx, executor, event))
	require.NotEmpty(t, executor.query)
	require.Equal(t, "failure", executor.args[11])
	require.Equal(t, 20101, *executor.args[15].(*int))
	require.Equal(t, "用户名或密码错误", executor.args[16])
}

func TestAuditSemanticsNestedChangesAndSensitiveVariants(t *testing.T) {
	before := map[string]any{"items": map[string]any{"first": "old", "second": "same"}, "version": 1}
	after := map[string]any{"items": map[string]any{"first": "new", "second": "same"}, "version": 2}
	old, next, changed := Changes(before, after)
	require.True(t, changed)
	require.Equal(t, map[string]any{"items": map[string]any{"first": "old"}}, old)
	require.Equal(t, map[string]any{"items": map[string]any{"first": "new"}}, next)
	for _, key := range []string{"passwordHash", "Access-Token", "credential_ciphertext", "client_secret_hash", "api_key", "receiver_mobile", "shipping_address"} {
		require.True(t, SensitiveKey(key), key)
	}
	require.False(t, SensitiveKey("phone_verified"))
}
