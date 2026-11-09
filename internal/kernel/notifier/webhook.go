package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Webhook sends the approved metadata projection with a stable delivery identity.
type Webhook struct{ Settings Settings }

// Send signs metadata and never returns an endpoint or an upstream response body.
func (w Webhook) Send(ctx context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if err := w.Settings.Validate(); err != nil {
		return err
	}
	if !w.Settings.WebhookEnabled {
		return errors.New("notification webhook disabled")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return errors.New("notification encoding failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Settings.WebhookURL, bytes.NewReader(raw))
	if err != nil {
		return errors.New("notification request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Notification-ID", strconv.FormatInt(m.EventID, 10))
	if w.Settings.WebhookSecret != "" {
		mac := hmac.New(sha256.New, []byte(w.Settings.WebhookSecret))
		if _, err := mac.Write(raw); err != nil {
			return errors.New("notification signature failed")
		}
		request.Header.Set("X-Notification-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("notification webhook delivery failed")
	}
	_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	closeErr := response.Body.Close()
	if copyErr != nil || closeErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("notification webhook delivery failed")
	}
	return nil
}
