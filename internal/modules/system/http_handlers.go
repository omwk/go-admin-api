package system

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

func (h handler) handlers() map[string]gin.HandlerFunc {
	return map[string]gin.HandlerFunc{
		"GET /admin/settings": h.settingsAll, "GET /admin/settings/:group": h.settingsGroup, "PUT /admin/settings/:group": h.settingsUpdate,
		"GET /admin/dicts": h.dictList, "POST /admin/dicts": h.dictCreate, "PUT /admin/dicts/:id": h.dictUpdate, "DELETE /admin/dicts/:id": h.dictDelete,
		"GET /admin/dicts/:id/items": h.dictItems, "POST /admin/dicts/:id/items": h.itemCreate, "PUT /admin/dict-items/:id": h.itemUpdate, "DELETE /admin/dict-items/:id": h.itemDelete,
		"GET /meta/dicts/:type": h.metaDict, "GET /meta/error-codes": func(c *gin.Context) { httpx.OK(c, errs.All()) },
	}
}

func (h handler) settingsAll(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	out, err := h.settings.Groups(c.Request.Context(), actor(c))
	if !tenantSettingAllowed(c) {
		filtered := make([]SettingGroup, 0, len(out))
		for _, group := range out {
			if group.Group != "tenant" {
				filtered = append(filtered, group)
			}
		}
		out = filtered
	}
	respond(c, out, err)
}
func (h handler) settingsGroup(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	out, err := h.settings.Group(c.Request.Context(), actor(c), c.Param("group"))
	respond(c, out, err)
}
func (h handler) settingsUpdate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	write(h, c, func(ctx context.Context, a Actor, _ int64, r map[string]json.RawMessage) (any, int, error) {
		out, err := h.settings.Update(ctx, a, c.Param("group"), r)
		return out, 200, err
	})
}
func (h handler) dictList(c *gin.Context) {
	page, size, err := h.pagination(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	out, err := h.dicts.List(c.Request.Context(), actor(c), c.Query("scope"), page, size)
	respond(c, out, err)
}
func (h handler) dictItems(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, invalidHTTP())
		return
	}
	page, size, err := h.pagination(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	out, err := h.dicts.Items(c.Request.Context(), actor(c), id, page, size)
	respond(c, out, err)
}
func (h handler) dictCreate(c *gin.Context) {
	write(h, c, func(ctx context.Context, a Actor, _ int64, r DictRequest) (any, int, error) {
		out, err := h.dicts.Create(ctx, a, r)
		return out, 201, err
	})
}
func (h handler) dictUpdate(c *gin.Context) {
	write(h, c, func(ctx context.Context, a Actor, id int64, r DictRequest) (any, int, error) {
		out, err := h.dicts.Update(ctx, a, id, r)
		return out, 200, err
	})
}
func (h handler) dictDelete(c *gin.Context) {
	write(h, c, func(ctx context.Context, a Actor, id int64, _ struct{}) (any, int, error) {
		return nil, 204, h.dicts.Delete(ctx, a, id)
	})
}
func (h handler) itemCreate(c *gin.Context) {
	write(h, c, func(ctx context.Context, a Actor, id int64, r DictItemRequest) (any, int, error) {
		out, err := h.dicts.CreateItem(ctx, a, id, r)
		return out, 201, err
	})
}
func (h handler) itemUpdate(c *gin.Context) {
	write(h, c, func(ctx context.Context, a Actor, id int64, r DictItemRequest) (any, int, error) {
		out, err := h.dicts.UpdateItem(ctx, a, id, r)
		return out, 200, err
	})
}
func (h handler) itemDelete(c *gin.Context) {
	write(h, c, func(ctx context.Context, a Actor, id int64, _ struct{}) (any, int, error) {
		return nil, 204, h.dicts.DeleteItem(ctx, a, id)
	})
}
func (h handler) metaDict(c *gin.Context) {
	out, err := h.dicts.Meta(c.Request.Context(), actor(c), c.Param("type"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.Header("Cache-Control", "private, max-age=60")
	c.Header("Vary", "Authorization")
	httpx.OK(c, out)
}
