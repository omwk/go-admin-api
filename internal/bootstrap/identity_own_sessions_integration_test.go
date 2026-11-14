package bootstrap

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/session"
	"github.com/stretchr/testify/require"
)

func sessionMetadataRequest(t *testing.T, f *accountFixture, path, ip, agent string, payload any) identity.Tokens {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	request := httptest.NewRequest("POST", "/api/v1/auth/"+path, bytes.NewReader(body)).WithContext(f.ctx)
	request.RemoteAddr = ip + ":1234"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", agent)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code)
	var tokens identity.Tokens
	notificationDataF3(t, response, &tokens)
	return tokens
}

func ownSessionMetadata(t *testing.T, f *accountFixture, bearer, query string) ([]map[string]any, int64) {
	t.Helper()
	response := f.request(t, "GET", "/auth/sessions"+query, bearer, nil)
	require.Equal(t, 200, response.Code)
	var page struct {
		List  []map[string]any
		Total int64
	}
	notificationDataF3(t, response, &page)
	for _, row := range page.List {
		require.Len(t, row, 7, "own sessions expose exactly the documented safe fields")
		for _, key := range []string{"sid", "user_id", "created_at", "expires_at", "ip", "user_agent", "current"} {
			require.Contains(t, row, key)
		}
	}
	return page.List, page.Total
}

func TestSessionMetadataIntegrationTwoDevicesAndRefresh(t *testing.T) {
	// BE-IDN-003 §4.8-9/11: login/refresh metadata follows each family, with exactly one current device.
	f := newAccountFixture(t)
	require.Equal(t, 204, f.request(t, "POST", "/auth/logout", f.token, nil).Code)
	credentials := map[string]any{"account": f.user.Username, "password": f.password}
	first := sessionMetadataRequest(t, f, "login", "192.0.2.10", "device-one", credentials)
	second := sessionMetadataRequest(t, f, "login", "198.51.100.20", "device-two", credentials)
	firstClaims, err := f.service.Authenticate(f.ctx, first.AccessToken)
	require.NoError(t, err)
	secondClaims, err := f.service.Authenticate(f.ctx, second.AccessToken)
	require.NoError(t, err)
	want := map[string][]string{firstClaims.SessionID: {"192.0.2.10", "device-one"}, secondClaims.SessionID: {"198.51.100.20", "device-two"}}
	for _, tokens := range []identity.Tokens{first, second} {
		claims, err := f.service.Authenticate(f.ctx, tokens.AccessToken)
		require.NoError(t, err)
		rows, total := ownSessionMetadata(t, f, tokens.AccessToken, "")
		require.EqualValues(t, 2, total)
		require.Len(t, rows, 2)
		current := 0
		for _, row := range rows {
			sid := row["sid"].(string)
			require.Equal(t, want[sid][0], row["ip"])
			require.Equal(t, want[sid][1], row["user_agent"])
			require.Equal(t, sid == claims.SessionID, row["current"])
			if row["current"] == true {
				current++
			}
		}
		require.Equal(t, 1, current)
	}
	f.now = f.now.Add(time.Second)
	agent := strings.Repeat("a", 255) + "界" + strings.Repeat("z", 300)
	rotated := sessionMetadataRequest(t, f, "refresh", "203.0.113.30", agent, map[string]any{"refresh_token": second.RefreshToken})
	rows, total := ownSessionMetadata(t, f, rotated.AccessToken, "")
	require.EqualValues(t, 2, total, "rotation retains one row per family")
	for _, row := range rows {
		if row["sid"] == secondClaims.SessionID {
			require.Equal(t, true, row["current"])
			require.Equal(t, "203.0.113.30", row["ip"])
			require.Equal(t, strings.Repeat("a", 255), row["user_agent"])
			require.True(t, utf8.ValidString(row["user_agent"].(string)))
		}
	}
	t.Log("two devices each see exactly one current=true; refresh retains sid and returns new bounded IP/UA")
}

func addLegacyOwnSession(t *testing.T, f *accountFixture, user int64) string {
	t.Helper()
	sid, err := session.NewID()
	require.NoError(t, err)
	raw, err := refresh.Generate()
	require.NoError(t, err)
	row := model.RefreshToken{TenantID: 1, UserID: user, SessionID: sid, TokenHash: refresh.Hash(raw), CreatedAt: f.now, ExpiresAt: f.now.Add(time.Hour)}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Omit("IP", "UserAgent").Create(&row).Error)
	return sid
}

func TestSessionMetadataIntegrationLegacyOwnershipAndPagination(t *testing.T) {
	// BE-IDN-003 §4.10-11: old rows use null/false, while foreign users' sessions never appear.
	f := newAccountFixture(t)
	legacy := addLegacyOwnSession(t, f, f.user.ID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	other := model.User{TenantID: 1, Username: "session-other", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active"}
	require.NoError(t, q.Create(&other).Error)
	foreign := addLegacyOwnSession(t, f, other.ID)
	rows, total := ownSessionMetadata(t, f, f.token, "")
	require.EqualValues(t, 2, total)
	legacyFound := false
	for _, row := range rows {
		require.NotEqual(t, foreign, row["sid"])
		if row["sid"] == legacy {
			legacyFound = true
			require.Nil(t, row["ip"])
			require.Nil(t, row["user_agent"])
			require.Equal(t, false, row["current"])
		}
	}
	require.True(t, legacyFound)
	first, total := ownSessionMetadata(t, f, f.token, "?page=1&page_size=1")
	require.EqualValues(t, 2, total)
	second, _ := ownSessionMetadata(t, f, f.token, "?page=2&page_size=1")
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0]["sid"], second[0]["sid"])
	require.Equal(t, 401, f.request(t, "GET", "/auth/sessions", "", nil).Code)
	t.Log("legacy record => ip=null, user_agent=null, current=false; ownership and pagination preserved; exactly seven safe keys")
}
