package channel

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func base64Signature(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func (s *httpSender) sendAliyun(ctx context.Context, m Message) error {
	v := url.Values{}
	action, version := "SingleSendMail", "2015-11-23"
	if s.kind == "aliyun_dm" {
		v.Set("AccountName", s.config["from_address"])
		v.Set("AddressType", "1")
		v.Set("ReplyToAddress", "false")
		v.Set("ToAddress", m.Recipient)
		v.Set("Subject", m.Subject)
		v.Set("FromAlias", s.config["from_name"])
		bodyKey := "TextBody"
		if m.Format == "html" {
			bodyKey = "HtmlBody"
		}
		v.Set(bodyKey, m.Body)
	} else {
		if len(m.Recipient) < 3 || len(m.Recipient) > 20 || strings.Trim(m.Recipient, "+0123456789") != "" {
			return errors.New("invalid notification recipient")
		}
		action, version = "SendSms", "2017-05-25"
		v.Set("PhoneNumbers", m.Recipient)
		v.Set("SignName", s.config["sign_name"])
		v.Set("TemplateCode", s.config["template_code"])
		raw, err := json.Marshal(m.Variables)
		if err != nil {
			return errors.New("notification encoding failed")
		}
		v.Set("TemplateParam", string(raw))
	}
	body := v.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config["endpoint"], strings.NewReader(body))
	if err != nil {
		return errors.New("notification request failed")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := s.signAliyun(req, action, version, body); err != nil {
		return err
	}
	return s.do(req)
}

func (s *httpSender) signAliyun(req *http.Request, action, version, body string) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return errors.New("notification nonce failed")
	}
	digest := sha256.Sum256([]byte(body))
	req.Header.Set("x-acs-action", action)
	req.Header.Set("x-acs-version", version)
	req.Header.Set("x-acs-date", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	req.Header.Set("x-acs-signature-nonce", hex.EncodeToString(nonce[:]))
	req.Header.Set("x-acs-content-sha256", hex.EncodeToString(digest[:]))
	req.Header.Set("x-acs-region-id", s.config["region"])
	keys := []string{"host", "x-acs-action", "x-acs-content-sha256", "x-acs-date", "x-acs-region-id", "x-acs-signature-nonce", "x-acs-version"}
	headers := "host:" + req.URL.Host + "\n"
	for _, key := range keys[1:] {
		headers += key + ":" + req.Header.Get(key) + "\n"
	}
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := req.Method + "\n" + path + "\n" + strings.ReplaceAll(req.URL.Query().Encode(), "+", "%20") + "\n" + headers + "\n" + strings.Join(keys, ";") + "\n" + hex.EncodeToString(digest[:])
	requestHash := sha256.Sum256([]byte(canonical))
	mac := hmac.New(sha256.New, []byte(s.config["access_key_secret"]))
	if _, err := mac.Write([]byte("ACS3-HMAC-SHA256\n" + hex.EncodeToString(requestHash[:]))); err != nil {
		return errors.New("notification signature failed")
	}
	req.Header.Set("Authorization", "ACS3-HMAC-SHA256 Credential="+s.config["access_key_id"]+",SignedHeaders="+strings.Join(keys, ";")+",Signature="+hex.EncodeToString(mac.Sum(nil)))
	return nil
}
