package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

type adminHandler struct{ service *AdminService }
type noAdminBody struct{}

func adminActor(c *gin.Context) Overview { return c.MustGet(actorKey).(Overview) }

func adminID(c *gin.Context) (int64, error) {
	if c.Param("id") == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalidAdmin()
	}
	return id, nil
}

func adminDecode[T any](c *gin.Context) (T, []byte, error) {
	var value T
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return value, nil, invalidAdmin()
	}
	if len(body) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return value, nil, invalidAdmin()
		}
		var extra any
		if !errors.Is(decoder.Decode(&extra), io.EOF) {
			return value, nil, invalidAdmin()
		}
	}
	if binding.Validator.ValidateStruct(value) != nil {
		return value, nil, invalidAdmin()
	}
	return value, body, nil
}

func adminWrite[T any](h adminHandler, c *gin.Context, run func(context.Context, Overview, int64, T) (any, int, error)) {
	id, err := adminID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	request, body, err := adminDecode[T](c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if len(key) > 256 {
		httpx.Fail(c, invalidAdmin())
		return
	}
	if key != "" {
		key = adminDigest([]byte(key))
	}
	actor := adminActor(c)
	scope := strconv.FormatInt(actor.User.ID, 10) + ":" + c.Request.Method + ":" + c.Request.URL.Path
	result, err := h.service.Execute(c.Request.Context(), actor, scope, key, adminDigest(body), func(ctx context.Context) (repo.SavedResponse, error) {
		value, status, err := run(ctx, actor, id, request)
		if err != nil {
			return repo.SavedResponse{}, err
		}
		data, err := json.Marshal(value)
		return repo.SavedResponse{Status: status, Data: data}, err
	})
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if result.InvalidationPending {
		c.Header("X-Authz-Invalidation", "pending")
	}
	switch result.Status {
	case 201:
		httpx.Created(c, result.Data)
	case 204:
		httpx.NoContent(c)
	default:
		httpx.OK(c, result.Data)
	}
}

func adminDigest(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func adminPagination(c *gin.Context, roles bool) (repo.Page, error) {
	page := repo.Page{Number: 1, Size: 20, Sort: "-created_at"}
	if roles {
		page.Sort = "-id"
	} // sys_role has no created_at column.
	for key, target := range map[string]*int{"page": &page.Number, "page_size": &page.Size} {
		if raw, exists := c.GetQuery(key); exists {
			number, err := strconv.Atoi(raw)
			if err != nil || number <= 0 {
				return page, invalidAdmin()
			}
			*target = number
		}
	}
	if page.Size > 100 {
		page.Size = 100
	}
	if page.Number > 10000 {
		return page, invalidAdmin()
	}
	if sort, exists := c.GetQuery("sort"); exists {
		page.Sort = sort
	}
	allowed := map[string]bool{"-created_at": true, "created_at": true, "username": true, "-username": true}
	if roles {
		allowed = map[string]bool{"-id": true, "id": true, "code": true, "-code": true}
	}
	if !allowed[page.Sort] {
		return page, invalidAdmin()
	}
	return page, nil
}
