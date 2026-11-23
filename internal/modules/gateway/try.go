package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

type trialContextKey struct{}
type trialContext struct {
	client Client
	route  Route
}

// Try signs and executes through the real open handler without retaining bodies.
func (s *ObservationService) Try(ctx context.Context, scope ObservationScope, clientID int64, in TryInput, source string) (out TryResult, err error) {
	client, err := s.store.ObservationClient(ctx, scope, clientID)
	if err != nil {
		return out, err
	}
	if in.RouteID <= 0 {
		return out, Invalid()
	}
	route, err := s.store.ObservationRoute(ctx, client.TenantID, in.RouteID)
	if err != nil {
		return out, err
	}
	request, err := trialRequest(ctx, client, route, in, source)
	if err != nil {
		return out, err
	}
	open, closeOpen, err := s.dispatch(ctx)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, closeOpen()) }()
	capture := &trialCapture{header: make(http.Header)}
	started := time.Now()
	if err := open.auth.signTrial(request, client); err != nil {
		cause := open.rejectTrial(capture, request, "", "", err)
		open.write(capture, "", "", nil, cause)
	} else {
		open.ServeHTTP(capture, request)
	}
	out = capture.result(route)
	out.LatencyMS = max(0, time.Since(started).Milliseconds())
	return out, nil
}

func trialRequest(ctx context.Context, client Client, route Route, in TryInput, source string) (*http.Request, error) {
	path := in.Path
	if path == "" {
		path = route.Path
	}
	parsed, err := url.Parse(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(path, "/open/") || in.Method != "" && in.Method != route.Method {
		return nil, Invalid()
	}
	query := make(url.Values)
	for key, value := range in.Query {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	body := in.Body
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		body = nil
	}
	marker := trialContext{client: client, route: route}
	r, err := http.NewRequestWithContext(context.WithValue(ctx, trialContextKey{}, marker), route.Method, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return nil, Invalid()
	}
	capability, _, ok := matchCapability(r)
	if !ok || capability.Code != route.Capability || capability.Path != route.Path {
		return nil, Invalid()
	}
	r.RemoteAddr = source
	return r, nil
}

func (a *Authenticator) signTrial(r *http.Request, client Client) error {
	keys, err := a.store.Secrets(r.Context(), client.TenantID, client.ID)
	if err != nil {
		return err
	}
	now := a.now().UTC()
	for _, key := range keys {
		if key.Status != "active" || key.ExpiresAt != nil && !now.Before(*key.ExpiresAt) {
			continue
		}
		if key.TenantID != client.TenantID || key.ClientID != client.ID {
			return Unavailable()
		}
		plain, err := a.sealer.Open(key.CipherText, key.Nonce, secretAAD(key), key.KeyVersion)
		if err != nil {
			return Unavailable()
		}
		defer clear(plain)
		r.Header.Set("X-App-Id", client.AppID)
		r.Header.Set("X-Timestamp", strconv.FormatInt(now.Unix(), 10))
		nonce := sha256.Sum256([]byte(ulid.Make().String()))
		r.Header.Set("X-Nonce", hex.EncodeToString(nonce[:16]))
		r.Header.Set("X-Signature-Version", "2")
		r.Header.Set("Idempotency-Key", ulid.Make().String())
		body, err := boundedBody(r)
		if err != nil {
			return err
		}
		canonical, err := SigningRequest(r, r.Header.Get("X-Timestamp"), r.Header.Get("X-Nonce"), body)
		if err != nil {
			return err
		}
		mac := hmac.New(sha256.New, plain)
		mac.Write([]byte(canonical))
		r.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		return nil
	}
	return authenticationFailure(80102)
}

func (h *OpenHandler) rejectTrial(w http.ResponseWriter, r *http.Request, trace, request string, cause error) error {
	trial, ok := r.Context().Value(trialContextKey{}).(trialContext)
	if !ok {
		return cause
	}
	if trace == "" {
		trace = ulid.Make().String()
	}
	p := Principal{TenantID: trial.client.TenantID, ClientID: trial.client.ID, AppID: trial.client.AppID}
	call := h.calls.NewCall(p, trial.route, trace, request, h.auth.source(r))
	call.ChargeSource = "manual_test"
	w.Header().Set("X-Call-Id", call.ID)
	return h.calls.Reject(r.Context(), call, cause)
}
