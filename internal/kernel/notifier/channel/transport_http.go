package channel

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
	"net/url"
	"strconv"
	"time"
)

type httpSender struct {
	channel, kind string
	config        Config
}

func (s *httpSender) Channel() string                { return s.channel }
func (s *httpSender) Kind() string                   { return s.kind }
func (s *httpSender) Test(ctx context.Context) error { return s.Send(ctx, testMessage(s.config)) }

func (s *httpSender) Send(ctx context.Context, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if s.channel == "email" && validateAddress(m.Recipient) != nil {
		return errors.New("invalid notification recipient")
	}
	if s.kind == "aliyun_dm" || s.kind == "aliyun_sms" {
		return s.sendAliyun(ctx, m)
	}
	raw, err := s.payload(m)
	if err != nil {
		return errors.New("notification encoding failed")
	}
	endpoint := s.config["endpoint"]
	if s.kind == "dingtalk_bot" && s.config["secret"] != "" {
		endpoint, err = dingEndpoint(endpoint, s.config["secret"])
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("notification request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	if s.kind == "sendgrid" {
		req.Header.Set("Authorization", "Bearer "+s.config["api_key"])
	}
	if s.kind == "http" {
		mac := hmac.New(sha256.New, []byte(s.config["secret"]))
		if _, err := mac.Write(raw); err != nil {
			return errors.New("notification signature failed")
		}
		req.Header.Set("X-Notification-Signature", hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-Notification-ID", strconv.FormatInt(m.ID, 10))
	}
	return s.do(req)
}

func (s *httpSender) payload(m Message) ([]byte, error) {
	switch s.kind {
	case "sendgrid":
		kind := "text/plain"
		if m.Format == "html" {
			kind = "text/html"
		}
		return json.Marshal(map[string]any{"personalizations": []any{map[string]any{"to": []any{map[string]string{"email": m.Recipient}}}}, "from": map[string]string{"email": s.config["from_address"], "name": s.config["from_name"]}, "subject": m.Subject, "content": []any{map[string]string{"type": kind, "value": m.Body}}})
	case "wecom_bot":
		return json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": m.Subject + "\n" + m.Body}})
	case "dingtalk_bot":
		return json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": m.Subject + "\n" + m.Body}})
	default:
		return json.Marshal(map[string]any{"id": m.ID, "subject": m.Subject, "body": m.Body, "format": m.Format})
	}
}

func (s *httpSender) do(req *http.Request) error {
	client := http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("notification provider request failed")
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(raw) > 65536 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("notification provider delivery failed")
	}
	if s.kind == "http" || s.kind == "sendgrid" {
		return nil
	}
	var result struct {
		ErrCode   *int   `json:"errcode"`
		Code      string `json:"Code"`
		RequestID string `json:"RequestId"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return errors.New("notification provider response invalid")
	}
	if s.kind == "wecom_bot" || s.kind == "dingtalk_bot" {
		if result.ErrCode == nil || *result.ErrCode != 0 {
			return errors.New("notification robot rejected delivery")
		}
		return nil
	}
	if s.kind == "aliyun_sms" && result.Code != "OK" || s.kind == "aliyun_dm" && (result.Code != "" || result.RequestID == "") {
		return errors.New("notification provider rejected delivery")
	}
	return nil
}

func dingEndpoint(endpoint, secret string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.New("invalid notification endpoint")
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(timestamp + "\n" + secret)); err != nil {
		return "", errors.New("notification signature failed")
	}
	q := u.Query()
	q.Set("timestamp", timestamp)
	q.Set("sign", base64Signature(mac.Sum(nil)))
	u.RawQuery = q.Encode()
	return u.String(), nil
}
