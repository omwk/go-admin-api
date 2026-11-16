package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// RequestMeta holds the request source shared by all audit producers.
type RequestMeta struct{ IP, UserAgent, RequestID string }

type requestMetaKey struct{}

// WithRequestMeta carries request metadata across service and transaction boundaries.
func WithRequestMeta(ctx context.Context, meta RequestMeta) context.Context {
	return context.WithValue(ctx, requestMetaKey{}, meta)
}

// RequestMetaFromContext returns source metadata, or its zero value for background work.
func RequestMetaFromContext(ctx context.Context) RequestMeta {
	meta, _ := ctx.Value(requestMetaKey{}).(RequestMeta)
	return meta
}

// Action converts a permission-style action to the registered dot form.
func Action(value string) string {
	return strings.NewReplacer(":", ".", "_", ".", "-", ".").Replace(value)
}

// Label bounds display snapshots without splitting Unicode characters.
func Label(value string) string {
	runes := []rune(value)
	if len(runes) > 128 {
		return string(runes[:128])
	}
	return value
}

// ResourceName extracts a display label before state diffs discard unchanged names.
func ResourceName(resource string, before, after map[string]any) string {
	keys := []string{"name", "title", "code"}
	switch resource {
	case "user":
		keys = []string{"username"}
	case "dict":
		keys = []string{"type"}
	case "dict_item":
		keys = []string{"label"}
	}
	for _, state := range []map[string]any{before, after} {
		for _, key := range keys {
			if value, ok := state[key].(string); ok && value != "" {
				return Label(value)
			}
		}
	}
	return ""
}

// Changes compares original values before redaction and returns only changed fields.
func Changes(before, after map[string]any) (map[string]any, map[string]any, bool) {
	old, next := map[string]any{}, map[string]any{}
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	for key := range keys {
		if key == "updated_at" || key == "version" {
			continue
		}
		leftMap, leftOK := before[key].(map[string]any)
		rightMap, rightOK := after[key].(map[string]any)
		if leftOK && rightOK {
			if left, right, changed := Changes(leftMap, rightMap); changed {
				old[key], next[key] = left, right
			}
			continue
		}
		left, leftErr := json.Marshal(before[key])
		right, rightErr := json.Marshal(after[key])
		if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
			old[key], next[key] = before[key], after[key]
		}
	}
	return old, next, len(old) > 0
}

// Failure stores a stable public error description, never the cause or arbitrary text.
func Failure(event *Event, failure error) {
	code := errs.ErrInternal
	var app *errs.AppError
	if errors.As(failure, &app) {
		code = app.Code
	}
	event.Result, event.ErrorCode = "failure", &code
	event.ErrorMessage = failureMessage(code)
}

func failureMessage(code int) string {
	switch code {
	case errs.ErrInvalidParam:
		return "参数校验失败"
	case errs.ErrNotFound:
		return "资源不存在"
	case errs.ErrConflict, 10409:
		return "资源冲突或仍被引用"
	case errs.ErrTooManyRequests:
		return "请求过于频繁"
	case 20101:
		return "用户名或密码错误"
	case 20102, 20103:
		return "认证已失效"
	case 20104:
		return "账号锁定"
	case 20107, 20108:
		return "无权限或超出访问范围"
	default:
		return "操作失败"
	}
}

func prepare(ctx context.Context, event Event) Event {
	meta := RequestMetaFromContext(ctx)
	if event.IP == "" {
		event.IP = meta.IP
	}
	if event.UserAgent == "" {
		event.UserAgent = meta.UserAgent
	}
	event.Action = Action(event.Action)
	event.ActorName, event.ActorTenantName, event.ResourceLabel = Label(event.ActorName), Label(event.ActorTenantName), Label(event.ResourceLabel)
	if event.Result == "failure" {
		if event.ErrorCode == nil {
			Failure(&event, nil)
		} else {
			event.ErrorMessage = failureMessage(*event.ErrorCode)
		}
	}
	return event
}

func redact(value map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var sanitized any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&sanitized); err != nil {
		return nil, err
	}
	redactValue(sanitized)
	return json.Marshal(sanitized)
}

func redactValue(value any) {
	switch nested := value.(type) {
	case map[string]any:
		for key, item := range nested {
			if SensitiveKey(key) {
				nested[key] = "***"
			} else {
				redactValue(item)
			}
		}
	case []any:
		for _, item := range nested {
			redactValue(item)
		}
	}
}

// SensitiveKey identifies values that must never be stored in audit snapshots.
func SensitiveKey(key string) bool {
	key = strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	for _, suffix := range []string{"passwordhash", "password", "cipher", "ciphertext", "nonce", "secret", "secrethash", "token", "tokenhash", "apikey", "accesskey", "privatekey", "phone", "mobile", "address"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}
