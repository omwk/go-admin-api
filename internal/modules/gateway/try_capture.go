package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"unicode/utf8"
)

// MaxTrialResponseBytes bounds the retained public trial response to 256 KiB.
const MaxTrialResponseBytes = 256 << 10

type trialCapture struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	truncated bool
}

func (c *trialCapture) Header() http.Header { return c.header }

func (c *trialCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *trialCapture) Write(data []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	n := len(data)
	remaining := MaxTrialResponseBytes - c.body.Len()
	if n > remaining {
		data, c.truncated = data[:remaining], true
	}
	_, err := c.body.Write(data)
	return n, err
}

func (c *trialCapture) result(route Route) TryResult {
	out := TryResult{StatusCode: c.status, Test: true, CallID: c.header.Get("X-Call-Id"), Truncated: c.truncated,
		Billing: TryBilling{Mode: route.BillingMode, Units: route.UnitsPerCall}}
	data := c.body.Bytes()
	if c.truncated {
		for !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		out.Response = string(data)
		return out
	}
	var envelope struct {
		Code int `json:"code"`
		Data any `json:"data"`
	}
	if json.Unmarshal(data, &envelope) == nil {
		out.ErrorCode, out.Response = envelope.Code, envelope.Data
		if envelope.Code != 0 {
			var response any
			if json.Unmarshal(data, &response) == nil {
				out.Response = response
			}
		}
	}
	return out
}
