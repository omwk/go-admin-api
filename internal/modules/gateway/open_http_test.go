package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type openTestStore struct {
	route   Route
	granted bool
	err     error
}

func (s *openTestStore) Route(context.Context, Principal, string) (Route, error) {
	return s.route, s.err
}
func (s *openTestStore) RouteGranted(context.Context, Principal, int64, time.Time) (bool, error) {
	return s.granted, s.err
}

type openTestOrders struct {
	calls int
	err   error
	after func()
}

func (s *openTestOrders) OrderStatus(_ context.Context, _ Principal, id string) (OpenOrderStatus, error) {
	s.calls++
	if s.after != nil {
		s.after()
	}
	return OpenOrderStatus{ID: id, OrderNo: "display-order", Status: "completed", UpdatedAt: time.Unix(1800000000, 0).UTC()}, s.err
}

func openTestHandler(t *testing.T) (*authFixture, *openTestStore, *openTestOrders, *OpenHandler) {
	t.Helper()
	f := newAuthFixture(t)
	c := Capabilities()[0]
	s := &openTestStore{route: Route{ID: 1, TenantID: 1, Code: c.Code, Capability: c.Code, Method: c.Method, Path: c.Path, RouteGroup: c.Group, Visibility: "public", Status: "enabled", BillingMode: "free", Version: 1}, granted: true}
	o := &openTestOrders{}
	calls, _ := newCallTestService(t)
	h, err := NewOpenHandler(f.auth, s, o, calls, openTestLimiter{}, func() time.Time { return f.now })
	require.NoError(t, err)
	return f, s, o, h
}

func openTestRequest(t *testing.T, f *authFixture, path string) *http.Request {
	t.Helper()
	r := f.request(t, f.text, 0)
	r.URL.Path = path
	r.URL.RawQuery = ""
	canonical, err := CanonicalRequest(r, r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), nil)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, []byte(f.text))
	mac.Write([]byte(canonical))
	r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r
}

func TestOpenRouteDoubleGate(t *testing.T) {
	// REQ-GW-001/004/006: primary data cannot create a code capability.
	for _, tc := range []struct {
		name, path   string
		edit         func(*openTestStore)
		status, code int
	}{
		{"enabled", "", func(*openTestStore) {}, 200, 0},
		{"unknown", "/open/v1/internal", func(*openTestStore) {}, 404, 80201},
		{"disabled", "", func(s *openTestStore) { s.route.Status = "disabled" }, 404, 80201},
		{"private", "", func(s *openTestStore) { s.route.Visibility = "private" }, 404, 80201},
		{"unregistered handler", "", func(s *openTestStore) { s.route.Capability = "admin.raw" }, 404, 80201},
		{"paid", "", func(s *openTestStore) { s.route.BillingMode = "per_call" }, 404, 80201},
		{"different tenant", "", func(s *openTestStore) { s.route.TenantID = 2 }, 404, 80201},
		{"not granted", "", func(s *openTestStore) { s.granted = false }, 403, 80104},
		{"database outage", "", func(s *openTestStore) { s.err = Unavailable() }, 503, 10002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, s, o, h := openTestHandler(t)
			tc.edit(s)
			path := tc.path
			if path == "" {
				path = "/open/v1/orders/" + ulid.Make().String() + "/status"
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, openTestRequest(t, f, path))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.EqualValues(t, tc.code, body["code"])
			if tc.status != 200 {
				require.Zero(t, o.calls)
				require.NotContains(t, body, "data")
			} else {
				require.Equal(t, 1, o.calls)
			}
			if tc.status == 404 {
				require.Equal(t, "接口不可用", body["message"])
			}
		})
	}
}

func TestOpenDisclosureRechecksAndAdminIsolation(t *testing.T) {
	f, s, o, h := openTestHandler(t)
	o.after = func() { s.route.Status = "disabled" }
	var adminCalls int
	stack := Dispatch(h, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { adminCalls++; w.WriteHeader(204) }))
	w := httptest.NewRecorder()
	stack.ServeHTTP(w, openTestRequest(t, f, "/open/v1/orders/"+ulid.Make().String()+"/status"))
	require.Equal(t, 404, w.Code)
	require.Zero(t, adminCalls)
	require.NotContains(t, w.Body.String(), "display-order")
	w = httptest.NewRecorder()
	stack.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/users", nil))
	require.Equal(t, 204, w.Code)
	require.Equal(t, 1, adminCalls)
}

func TestOpenContractMatchesCapabilitiesAndExactFields(t *testing.T) {
	var spec struct {
		Paths      map[string]map[string]any
		Components struct {
			Schemas map[string]struct {
				AdditionalProperties bool `yaml:"additionalProperties"`
				Properties           map[string]any
			}
		}
	}
	require.NoError(t, yaml.Unmarshal(OpenAPI, &spec))
	require.Len(t, spec.Paths, 1)
	c := Capabilities()[0]
	require.Equal(t, "GET", c.Method)
	require.Contains(t, spec.Paths, "/open/v1/orders/{id}/status")
	require.ElementsMatch(t, []string{"id", "order_no", "status", "updated_at"}, schemaKeys(spec.Components.Schemas["OrderStatus"].Properties))
	for _, name := range []string{"OrderStatus", "StatusResponse", "ErrorResponse"} {
		require.False(t, spec.Components.Schemas[name].AdditionalProperties)
	}
	f, _, _, h := openTestHandler(t)
	r := openTestRequest(t, f, "/open/v1/orders/"+ulid.Make().String()+"/status")
	r.Header.Set("X-Request-Id", "unsafe\ncanary")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.ElementsMatch(t, []string{"code", "message", "data", "trace_id"}, schemaKeys(envelope))
	var data map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["data"], &data))
	require.ElementsMatch(t, []string{"id", "order_no", "status", "updated_at"}, schemaKeys(data))
}

func schemaKeys[T any](items map[string]T) []string {
	out := make([]string, 0, len(items))
	for key := range items {
		out = append(out, key)
	}
	return out
}

func TestPublicErrorDropsInternalMessagesAndDetails(t *testing.T) {
	for _, code := range []int{10001, 80101, 80102, 80103, 80104, 80105, 80201, 50101, 20108, 10006, 10004, 10409, 10005, 30302, 60402, 60403, 80302, 30501, 50401, 10002} {
		in := errs.New(code, 500, "internal-canary")
		in.Details = map[string]any{"tenant_id": "sensitive-canary", "parameter": "internal-canary"}
		out := errs.From(PublicError(in))
		require.NotContains(t, out.Message, "canary")
		require.Empty(t, out.Details)
		require.Nil(t, out.Cause)
	}
}

func TestPublicUpstreamFailurePreservesStatus(t *testing.T) {
	for _, status := range []int{502, 503} {
		in := errs.New(80301, status, "internal-canary")
		in.Details = map[string]any{"upstream": "internal-canary"}
		out := errs.From(PublicError(in))
		require.Equal(t, 80301, out.Code)
		require.Equal(t, status, out.HTTPStatus)
		require.Equal(t, "服务暂不可用", out.Message)
		require.Empty(t, out.Details)
	}
}
