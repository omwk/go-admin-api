package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSevenTransportsHTTPWireProtocols(t *testing.T) {
	for _, kind := range []string{"sendgrid", "aliyun_dm", "aliyun_sms", "wecom_bot", "dingtalk_bot", "http"} {
		t.Run(kind, func(t *testing.T) {
			secret := randomValue(t)
			recipient := randomValue(t) + "@example.invalid"
			cfg := Config{"api_key": secret, "secret": secret, "access_key_id": randomValue(t), "access_key_secret": secret, "region": "cn-hangzhou", "from_address": randomValue(t) + "@example.invalid", "sign_name": "mock", "template_code": "mock"}
			if kind == "aliyun_sms" {
				recipient = "+" + strconv.FormatUint(uint64(len(secret))*100000001+17, 10)
			}
			msg := Message{ID: 19, Recipient: recipient, Subject: "Subject", Body: "Body", Format: "text", Variables: map[string]string{"Name": "Ada"}}
			var called int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				require.Equal(t, http.MethodPost, r.Method)
				verifyWire(t, kind, secret, cfg, msg, r)
				switch kind {
				case "aliyun_dm":
					_, err := io.WriteString(w, `{"RequestId":"mock"}`)
					require.NoError(t, err)
				case "aliyun_sms":
					_, err := io.WriteString(w, `{"Code":"OK"}`)
					require.NoError(t, err)
				case "wecom_bot", "dingtalk_bot":
					_, err := io.WriteString(w, `{"errcode":0}`)
					require.NoError(t, err)
				default:
					w.WriteHeader(202)
				}
			}))
			defer server.Close()
			cfg["endpoint"] = server.URL
			ch := map[string]string{"sendgrid": "email", "aliyun_dm": "email", "aliyun_sms": "sms", "wecom_bot": "wecom", "dingtalk_bot": "dingtalk", "http": "webhook"}[kind]
			sender, err := NewSender(ch, kind, cfg)
			require.NoError(t, err)
			require.Equal(t, ch, sender.Channel())
			require.Equal(t, kind, sender.Kind())
			require.NoError(t, sender.Send(context.Background(), msg))
			require.Equal(t, 1, called)
		})
	}
}

func verifyWire(t *testing.T, kind, secret string, cfg Config, msg Message, r *http.Request) {
	t.Helper()
	if kind == "aliyun_dm" || kind == "aliyun_sms" {
		require.NoError(t, r.ParseForm())
		v := r.PostForm
		verifyAliyunSignature(t, r, secret, cfg, v.Encode())
		if kind == "aliyun_dm" {
			require.Equal(t, msg.Recipient, v.Get("ToAddress"))
			require.Equal(t, msg.Body, v.Get("TextBody"))
		} else {
			require.Equal(t, msg.Recipient, v.Get("PhoneNumbers"))
			require.Equal(t, cfg["template_code"], v.Get("TemplateCode"))
		}
		return
	}
	raw, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	switch kind {
	case "sendgrid":
		require.Equal(t, "Bearer "+secret, r.Header.Get("Authorization"))
		require.Contains(t, string(raw), msg.Recipient)
		require.Equal(t, msg.Subject, body["subject"])
	case "http":
		mac := hmac.New(sha256.New, []byte(secret))
		_, err := mac.Write(raw)
		require.NoError(t, err)
		require.Equal(t, hex.EncodeToString(mac.Sum(nil)), r.Header.Get("X-Notification-Signature"))
		require.NotContains(t, string(raw), msg.Recipient)
	case "dingtalk_bot":
		timestamp := r.URL.Query().Get("timestamp")
		require.NotEmpty(t, timestamp)
		mac := hmac.New(sha256.New, []byte(secret))
		_, err := mac.Write([]byte(timestamp + "\n" + secret))
		require.NoError(t, err)
		require.Equal(t, base64.StdEncoding.EncodeToString(mac.Sum(nil)), r.URL.Query().Get("sign"))
	case "wecom_bot":
		require.Equal(t, "text", body["msgtype"])
	}
}

func verifyAliyunSignature(t *testing.T, r *http.Request, secret string, cfg Config, body string) {
	t.Helper()
	digest := sha256.Sum256([]byte(body))
	require.Equal(t, hex.EncodeToString(digest[:]), r.Header.Get("x-acs-content-sha256"))
	keys := []string{"host", "x-acs-action", "x-acs-content-sha256", "x-acs-date", "x-acs-region-id", "x-acs-signature-nonce", "x-acs-version"}
	headers := "host:" + r.Host + "\n"
	for _, key := range keys[1:] {
		headers += key + ":" + r.Header.Get(key) + "\n"
	}
	canonical := "POST\n/\n\n" + headers + "\n" + strings.Join(keys, ";") + "\n" + hex.EncodeToString(digest[:])
	hash := sha256.Sum256([]byte(canonical))
	mac := hmac.New(sha256.New, []byte(secret))
	_, err := mac.Write([]byte("ACS3-HMAC-SHA256\n" + hex.EncodeToString(hash[:])))
	require.NoError(t, err)
	expected := "ACS3-HMAC-SHA256 Credential=" + cfg["access_key_id"] + ",SignedHeaders=" + strings.Join(keys, ";") + ",Signature=" + hex.EncodeToString(mac.Sum(nil))
	require.Equal(t, expected, r.Header.Get("Authorization"))
}

func TestHTTPTransportErrorsAndCancellationAreSanitized(t *testing.T) {
	secret := randomValue(t)
	for _, kind := range []string{"http", "wecom_bot", "aliyun_sms"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, err := io.WriteString(w, `{"errcode":1,"Code":"Denied","message":"`+secret+`"}`)
				require.NoError(t, err)
			}))
			defer server.Close()
			cfg := Config{"endpoint": server.URL + "?key=" + secret, "secret": secret, "access_key_id": randomValue(t), "access_key_secret": secret, "region": "mock", "sign_name": "mock", "template_code": "mock"}
			ch := map[string]string{"http": "webhook", "wecom_bot": "wecom", "aliyun_sms": "sms"}[kind]
			sender, err := NewSender(ch, kind, cfg)
			require.NoError(t, err)
			msg := Message{Body: "body", Recipient: "+" + strconv.Itoa(len(secret)*10000001+13)}
			if kind != "http" {
				err = sender.Send(context.Background(), msg)
				require.Error(t, err)
				require.NotContains(t, err.Error(), secret)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err = sender.Send(ctx, msg)
			require.Error(t, err)
			require.NotContains(t, err.Error(), secret)
			require.NotContains(t, err.Error(), server.URL)
		})
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://127.0.0.1:1", 302) }))
	defer redirect.Close()
	sender, err := NewSender("webhook", "http", Config{"endpoint": redirect.URL, "secret": secret})
	require.NoError(t, err)
	require.Error(t, sender.Send(context.Background(), Message{}))
}
