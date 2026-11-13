package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

func actor(c *gin.Context) Actor {
	current := identity.AdminActor(c)
	return Actor{TenantID: current.User.TenantID, UserID: current.User.ID}
}
func invalidHTTP() error { return errs.New(errs.ErrInvalidParam, 400, "参数校验失败") }

func decodeRequest[T any](c *gin.Context) (T, []byte, error) {
	var result T
	if c.Request.Body == nil {
		return result, nil, invalidHTTP()
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return result, nil, invalidHTTP()
	}
	if len(body) == 0 {
		return result, body, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, nil, invalidHTTP()
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return result, nil, invalidHTTP()
	}
	return result, body, nil
}

func write[T any](h handler, c *gin.Context, run func(context.Context, Actor, int64, T) (any, int, error)) {
	var id int64
	if raw := c.Param("id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			httpx.Fail(c, invalidHTTP())
			return
		}
		id = parsed
	}
	request, body, err := decodeRequest[T](c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if len(key) > 256 {
		httpx.Fail(c, invalidHTTP())
		return
	}
	if key != "" {
		key = idem.DigestKey(key)
	}
	current := actor(c)
	scope := strconv.FormatInt(current.UserID, 10) + ":" + c.Request.Method + ":" + c.Request.URL.Path
	hash := idem.DigestRequest(current.TenantID, current.UserID, c.Request.Method, c.FullPath(), []string{c.Request.URL.Path}, body)
	result, err := h.write(c.Request.Context(), current, scope, key, hash, func(ctx context.Context) (idem.Result, error) {
		value, status, err := run(ctx, current, id, request)
		if err != nil {
			return idem.Result{}, err
		}
		return idem.JSONResult(status, value)
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if strings.HasPrefix(c.FullPath(), "/api/v1/admin/dict") {
		h.dicts.Invalidate(current.TenantID)
	}
	switch result.Status {
	case 201:
		httpx.Created(c, result.Body)
	case 204:
		httpx.NoContent(c)
	default:
		httpx.OK(c, result.Body)
	}
}

func (h handler) pagination(c *gin.Context) (int, int, error) {
	size, err := setting.DefaultPageSize(c.Request.Context(), h.settings, actor(c).TenantID)
	if err != nil {
		return 0, 0, err
	}
	page := 1
	for key, target := range map[string]*int{"page": &page, "page_size": &size} {
		if raw, ok := c.GetQuery(key); ok {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				return 0, 0, invalidHTTP()
			}
			*target = parsed
		}
	}
	if page > 10000 {
		return 0, 0, invalidHTTP()
	}
	if size > 100 {
		size = 100
	}
	return page, size, nil
}

func respond(c *gin.Context, value any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, value)
}
