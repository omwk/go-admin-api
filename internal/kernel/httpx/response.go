package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

type response struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	TraceID   string `json:"trace_id"`
	RequestID string `json:"request_id,omitempty"`
}

type errorResponse struct {
	Code      int            `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	TraceID   string         `json:"trace_id"`
	RequestID string         `json:"request_id,omitempty"`
}

// OK writes a successful response.
func OK(c *gin.Context, data any) { success(c, http.StatusOK, data) }

// Created writes a successful creation response.
func Created(c *gin.Context, data any) { success(c, http.StatusCreated, data) }

// NoContent writes a successful response without a body.
func NoContent(c *gin.Context) { c.Status(http.StatusNoContent) }

// Fail aborts the handler chain and writes only public application error fields.
func Fail(c *gin.Context, err error) {
	app := errs.From(err)
	c.AbortWithStatusJSON(app.HTTPStatus, errorResponse{
		Code: app.Code, Message: app.Message, Details: app.Details,
		TraceID: logger.TraceID(c.Request.Context()), RequestID: c.GetString("request_id"),
	})
}

func success(c *gin.Context, status int, data any) {
	c.JSON(status, response{
		Code: 0, Message: "ok", Data: data,
		TraceID: logger.TraceID(c.Request.Context()), RequestID: c.GetString("request_id"),
	})
}
