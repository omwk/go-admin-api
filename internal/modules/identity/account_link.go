package identity

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// AccountLink returns an absolute frontend URL; the opaque credential stays in its fragment.
func AccountLink(base, purpose string, tenant int64, code, raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("app.public_url must be an absolute HTTP(S) base URL")
	}
	if tenant <= 0 || code == "" {
		return "", errors.New("account link tenant is required")
	}
	path := "/auth/password/reset"
	if purpose == "invite" {
		path = "/auth/invite/accept"
	} else if purpose == "verify" {
		path = "/auth/register/verify"
	}
	u.Path += path
	u.RawPath = ""
	u.RawQuery = url.Values{"tenant": {code}, "tenant_id": {strconv.FormatInt(tenant, 10)}}.Encode()
	u.Fragment = "token=" + url.QueryEscape(raw)
	return u.String(), nil
}

func (s *AccountService) accountLink(ctx context.Context, purpose string, tenant int64, raw string) (string, error) {
	row, found, err := s.Store.TenantForLogin(ctx, "", tenant)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("account link tenant unavailable")
	}
	return AccountLink(s.PublicURL, purpose, row.ID, row.Code, raw)
}

func (s *AccountService) linkTenant(ctx context.Context, code string, legacyID, fallbackID int64) (int64, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		if legacyID > 0 {
			return legacyID, nil
		}
		return fallbackID, nil
	}
	if len(code) > 64 {
		return 0, invalidLink()
	}
	row, found, err := s.Store.TenantForLogin(ctx, code, 0)
	if err != nil {
		return 0, dependencyFailure(err)
	}
	if !found {
		return 0, invalidLink()
	}
	return row.ID, nil
}
