package idem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrMismatch indicates that a key was reused with a different request.
var ErrMismatch = errors.New("idempotency request mismatch")

// ErrProcessing indicates that another transaction currently owns the key.
var ErrProcessing = errors.New("idempotency request is processing")

// Input contains request metadata extracted by an HTTP adapter. Body is copied
// only for hashing and is never passed to a persistence implementation.
type Input struct {
	Key        string
	Method     string
	Route      string
	PathParams []string
	Body       []byte
}

// Operation is the persisted-safe representation of an idempotent request.
type Operation struct {
	TenantID    int64
	UserID      int64
	Scope       string
	KeyDigest   string
	RequestHash string
}

// Result is the successful semantic response saved for replay.
type Result struct {
	Status int
	Body   json.RawMessage
}

// ClaimAction tells a service whether to execute or replay a request.
type ClaimAction uint8

const (
	// Execute means this transaction owns a new processing placeholder.
	Execute ClaimAction = iota + 1
	// Replay means a committed successful response is available.
	Replay
)

// Claim is the result of atomically claiming or reading a key.
type Claim struct {
	Action ClaimAction
	Result Result
}

// Store is the transaction-bound persistence port. Implementations must not
// commit Claim or Complete independently from the caller's business work.
type Store interface {
	Claim(ctx context.Context, operation Operation) (Claim, error)
	Complete(ctx context.Context, operation Operation, result Result) (Result, error)
}

// Prepare validates request identity and computes the storage-safe operation.
func Prepare(tenantID, userID int64, input Input) (Operation, error) {
	if tenantID <= 0 || userID <= 0 {
		return Operation{}, errors.New("idempotency identity is required")
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	route := strings.TrimSpace(input.Route)
	if method == "" || route == "" {
		return Operation{}, errors.New("idempotency method and route are required")
	}
	key := strings.TrimSpace(input.Key)
	if key == "" {
		return Operation{}, errors.New("idempotency key is required")
	}
	if len(key) > 255 {
		return Operation{}, fmt.Errorf("idempotency key exceeds %d bytes", 255)
	}
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
		return Operation{}, errors.New("idempotency method is not writable")
	}
	return Operation{TenantID: tenantID, UserID: userID, Scope: Scope(userID, method, route),
		KeyDigest: DigestKey(key), RequestHash: DigestRequest(tenantID, userID, method, route, input.PathParams, input.Body)}, nil
}

// Scope returns the stable per-user operation namespace. The route is a fixed
// template, never the concrete request path.
func Scope(userID int64, method, route string) string {
	return fmt.Sprintf("%d:%s:%s", userID, strings.ToUpper(strings.TrimSpace(method)), strings.TrimSpace(route))
}

// DigestKey hashes the client key before it reaches persistence.
func DigestKey(key string) string { return digest([]byte(strings.TrimSpace(key))) }

// DigestRequest hashes all request identity fields with unambiguous framing.
// Only surrounding body whitespace is normalized; JSON is never parsed or
// reordered, so different member order or bytes remain different requests.
func DigestRequest(tenantID, userID int64, method, route string, pathParams []string, body []byte) string {
	var encoded []byte
	appendField := func(value []byte) {
		encoded = append(encoded, fmt.Sprintf("%d:", len(value))...)
		encoded = append(encoded, value...)
	}
	appendField([]byte(fmt.Sprintf("%d", tenantID)))
	appendField([]byte(fmt.Sprintf("%d", userID)))
	appendField([]byte(strings.ToUpper(strings.TrimSpace(method))))
	appendField([]byte(strings.TrimSpace(route)))
	appendField([]byte(fmt.Sprintf("%d", len(pathParams))))
	for _, param := range pathParams {
		appendField([]byte(param))
	}
	appendField(normalizeBody(body))
	return digest(encoded)
}

// JSONResult serializes a safe response value, using JSON null for nil data.
func JSONResult(status int, value any) (Result, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return Result{}, err
	}
	if value == nil {
		body = []byte("null")
	}
	return Result{Status: status, Body: body}, nil
}

// ResultJSON validates and copies a persisted response body.
func ResultJSON(status int, body []byte) (Result, error) {
	if len(body) == 0 {
		body = []byte("null")
	}
	if !json.Valid(body) {
		return Result{}, errors.New("idempotency response is not valid JSON")
	}
	return Result{Status: status, Body: append(json.RawMessage(nil), body...)}, nil
}

func normalizeBody(body []byte) []byte { return bytes.TrimSpace(body) }

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
