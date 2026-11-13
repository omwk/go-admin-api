package system

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

type auditReaderStub struct {
	rows   []AuditRecord
	filter AuditFilter
	err    error
}

func (s *auditReaderStub) AuditRecords(_ context.Context, _ int64, filter AuditFilter) ([]AuditRecord, int64, error) {
	s.filter = filter
	return s.rows, int64(len(s.rows)), s.err
}

func TestAuditLogsRedactNestedStatesAndKeepNumbers(t *testing.T) {
	// S4 `5.5: redact before truncation, recursively and without rounding IDs.
	private, err := refresh.Generate()
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]any{
		"password_hash": private, "cipher": private, "nonce": private,
		"nested": []any{map[string]any{"secret": private, "access_token": private, "PasswordHash": private}},
		"id":     json.Number("9007199254740993"), "name": "visible",
	})
	require.NoError(t, err)
	store := &auditReaderStub{rows: []AuditRecord{{Before: raw, After: json.RawMessage("null")}}}
	service, err := NewAuditService(store)
	require.NoError(t, err)
	page, err := service.AuditLogs(context.Background(), Actor{TenantID: 1}, AuditFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, page.List, 1)
	state := string(page.List[0].Before)
	require.NotContains(t, state, private)
	require.Contains(t, state, "9007199254740993")
	require.Contains(t, state, "visible")
	for _, key := range []string{"password_hash", "cipher", "nonce", "secret", "access_token", "PasswordHash"} {
		require.Contains(t, state, `"`+key+`":"***"`)
	}
	require.JSONEq(t, "null", string(page.List[0].After))
	require.False(t, page.List[0].Truncated)
}

func TestAuditLogsBoundCombinedSummariesAsValidUTF8JSON(t *testing.T) {
	// S4 `5.6: escaping and multibyte text count toward the combined 4 KiB limit.
	for _, both := range []bool{false, true} {
		raw, err := json.Marshal(map[string]string{"text": strings.Repeat("审计<&>\n", 2000)})
		require.NoError(t, err)
		after := json.RawMessage("{}")
		if both {
			after = raw
		}
		store := &auditReaderStub{rows: []AuditRecord{{Before: raw, After: after}}}
		service, err := NewAuditService(store)
		require.NoError(t, err)
		page, err := service.AuditLogs(context.Background(), Actor{TenantID: 1}, AuditFilter{Page: 1, PageSize: 20})
		require.NoError(t, err)
		row := page.List[0]
		require.True(t, row.Truncated)
		require.LessOrEqual(t, len(row.Before)+len(row.After), 4096)
		for _, state := range []json.RawMessage{row.Before, row.After} {
			require.True(t, json.Valid(state))
			require.True(t, utf8.Valid(state))
		}
	}
}

func TestLoginLogsPreserveUnknownAndForceAction(t *testing.T) {
	// S4 `5.8: unknown snapshots and actor metadata cannot supply a guessed account.
	actorID := int64(42)
	store := &auditReaderStub{rows: []AuditRecord{
		{ID: 1, ResourceID: "unknown", ActorID: &actorID, Username: "must-not-appear", Result: "failure"},
		{ID: 2, ResourceID: "42", ActorID: &actorID, Username: "known", Result: "success"},
	}}
	service, err := NewAuditService(store)
	require.NoError(t, err)
	page, err := service.LoginLogs(context.Background(), Actor{TenantID: 1}, AuditFilter{Page: 1, PageSize: 20, Action: "other"})
	require.NoError(t, err)
	require.Equal(t, "identity.login", store.filter.Action)
	require.Equal(t, "未知账号", page.List[0].Username)
	require.Nil(t, page.List[0].UserID)
	require.Equal(t, "unknown", page.List[0].ResourceID)
	require.Equal(t, &actorID, page.List[1].UserID)
	require.Equal(t, "known", page.List[1].Username)
}

func TestAuditLogsRejectInvalidFiltersAndPropagateFailures(t *testing.T) {
	store := &auditReaderStub{}
	service, err := NewAuditService(store)
	require.NoError(t, err)
	for _, filter := range []AuditFilter{
		{Page: 0, PageSize: 20}, {Page: 1, PageSize: 101},
		{Page: 10001, PageSize: 20}, {Page: 1, PageSize: 20, ActorID: -1},
		{Page: 1, PageSize: 20, Result: "invalid"},
	} {
		_, err := service.AuditLogs(context.Background(), Actor{TenantID: 1}, filter)
		require.Error(t, err)
	}
	store.err = errors.New("injected database failure")
	_, err = service.AuditLogs(context.Background(), Actor{TenantID: 1}, AuditFilter{Page: 1, PageSize: 20})
	require.ErrorIs(t, err, store.err)
	store.err = nil
	store.rows = []AuditRecord{{Before: json.RawMessage("{")}}
	_, err = service.AuditLogs(context.Background(), Actor{TenantID: 1}, AuditFilter{Page: 1, PageSize: 20})
	require.Error(t, err)
}
