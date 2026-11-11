package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
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
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data,omitempty"`
	Details   map[string]any  `json:"details,omitempty"`
	TraceID   string          `json:"trace_id"`
	RequestID string          `json:"request_id,omitempty"`
}

// OK writes a successful response.
func OK(c *gin.Context, data any) { success(c, http.StatusOK, data) }

// Created writes a successful creation response.
func Created(c *gin.Context, data any) { success(c, http.StatusCreated, data) }

// NoContent writes a successful response without a body.
func NoContent(c *gin.Context) { c.Status(http.StatusNoContent) }

// Serialize creates the exact public response body used by idempotent writes.
func Serialize(c *gin.Context, status int, data any) (idem.Result, error) {
	body, err := json.Marshal(response{
		Code: 0, Message: "ok", Data: data,
		TraceID: logger.TraceID(c.Request.Context()), RequestID: c.GetString("request_id"),
	})
	if err != nil {
		return idem.Result{}, err
	}
	return idem.Result{Status: status, Body: body}, nil
}

// WriteResult writes a previously serialized successful response verbatim.
func WriteResult(c *gin.Context, result idem.Result) {
	if result.Status == http.StatusNoContent {
		c.Status(result.Status)
		return
	}
	c.Data(result.Status, "application/json; charset=utf-8", result.Body)
}

// Fail aborts the handler chain and writes only public application error fields.
func Fail(c *gin.Context, err error) {
	c.Abort()
	if c.Writer.Written() {
		return
	}
	app := errs.From(err)
	cause := err
	if app.Cause != nil {
		cause = app.Cause
	}
	body := errorResponse{
		Code: app.Code, Message: app.Message, Details: app.Details,
		TraceID: logger.TraceID(c.Request.Context()), RequestID: c.GetString("request_id"),
	}
	if app.Code == errs.ErrInternal && app.Cause != nil {
		body.Data = json.RawMessage(`null`)
	}
	encoded, encodingErr := json.Marshal(body)
	if encodingErr != nil {
		app = errs.From(encodingErr)
		body.Code, body.Message, body.Details = app.Code, app.Message, nil
		body.Data = json.RawMessage(`null`)
	}
	c.Set("error_code", app.Code)
	if app.HTTPStatus >= http.StatusInternalServerError {
		ctx := c.Request.Context()
		logger.From(ctx).ErrorContext(ctx, "HTTP request failed", "module", "http", "action", "fail",
			"route", routeLabel(c), "status", app.HTTPStatus, "error_code", app.Code, "error", cause, "encoding_error", encodingErr)
	}
	if encodingErr != nil {
		c.JSON(app.HTTPStatus, body)
		return
	}
	c.Data(app.HTTPStatus, "application/json; charset=utf-8", encoded)
}

func success(c *gin.Context, status int, data any) {
	c.JSON(status, response{
		Code: 0, Message: "ok", Data: data,
		TraceID: logger.TraceID(c.Request.Context()), RequestID: c.GetString("request_id"),
	})
}
