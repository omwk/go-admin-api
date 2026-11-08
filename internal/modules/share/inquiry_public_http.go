package share

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

func inquiryHandler(svc *LinkService, tenant int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !inquiryOrigin(c) {
			publicFailure(c, AccessDenied())
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		var in InquiryInput
		if decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			publicFailure(c, AccessDenied())
			return
		}
		cookie, err := c.Cookie("share_access")
		if err != nil {
			publicFailure(c, AccessDenied())
			return
		}
		if err := svc.SubmitInquiry(c.Request.Context(), tenant, c.Param("token"), c.ClientIP(), cookie, c.GetHeader("Idempotency-Key"), in); err != nil {
			publicFailure(c, err)
			return
		}
		awaitPublicFloor(c)
		httpx.Created(c, struct {
			Submitted bool `json:"submitted"`
		}{true})
	}
}

func inquiryOrigin(c *gin.Context) bool {
	origin, err := url.Parse(c.GetHeader("Origin"))
	if err != nil || origin.Scheme != "https" || origin.Host != c.Request.Host || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return false
	}
	media := strings.TrimSpace(strings.Split(c.GetHeader("Content-Type"), ";")[0])
	fetch := c.GetHeader("Sec-Fetch-Site")
	return media == "application/json" && (fetch == "" || fetch == "same-origin")
}
