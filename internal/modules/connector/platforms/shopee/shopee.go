// Package shopee implements the product list/base-info transport protocol.
package shopee

import (
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
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/connector"
)

const listPath = "/api/v2/product/get_item_list"
const detailPath = "/api/v2/product/get_item_base_info"

type Platform struct {
	base   string
	client *http.Client
	now    func() time.Time
}

// New requires an explicitly configured endpoint; no real platform is contacted at startup.
func New(endpoint string, client *http.Client) (*Platform, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid Shopee endpoint")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Platform{base: strings.TrimRight(endpoint, "/"), client: &copyClient, now: time.Now}, nil
}

func (p *Platform) Code() string { return "shopee" }
func (p *Platform) Capabilities() connector.Capabilities {
	return connector.Capabilities{Resources: []connector.ResourceType{connector.ResourceProduct}, IncrementalModes: []string{"page"}}
}

type credentials struct{ partner, key, token, shop string }

func decodeCredentials(c connector.Credential) (credentials, error) {
	values := make([]string, 4)
	for i, name := range []string{"partner_id", "partner_key", "access_token", "shop_id"} {
		if json.Unmarshal(c.Values[name], &values[i]) != nil || values[i] == "" {
			return credentials{}, failure(connector.ErrAuthExpired, 0, 0)
		}
	}
	return credentials{values[0], values[1], values[2], values[3]}, nil
}

func (p *Platform) Fetch(ctx context.Context, c connector.Credential, request connector.FetchRequest) (connector.FetchResult, error) {
	if request.Resource != connector.ResourceProduct || request.PageSize < 1 || request.PageSize > 100 {
		return connector.FetchResult{}, failure(connector.ErrInvalidRequest, 0, 0)
	}
	cred, err := decodeCredentials(c)
	if err != nil {
		return connector.FetchResult{}, err
	}
	offset := 0
	if request.Cursor != "" {
		offset, err = strconv.Atoi(request.Cursor)
		if err != nil || offset < 0 {
			return connector.FetchResult{}, failure(connector.ErrInvalidRequest, 0, 0)
		}
	}
	var page struct {
		Item []struct {
			ID json.Number `json:"item_id"`
		} `json:"item"`
		HasNext bool `json:"has_next_page"`
		Next    int  `json:"next_offset"`
	}
	params := url.Values{"offset": {strconv.Itoa(offset)}, "page_size": {strconv.Itoa(request.PageSize)}, "item_status": {"NORMAL", "BANNED", "DELETED", "UNLIST"}}
	if err := p.get(ctx, cred, listPath, params, &page); err != nil {
		return connector.FetchResult{}, err
	}
	if page.HasNext && page.Next <= offset {
		return connector.FetchResult{}, failure(connector.ErrInvalidRequest, 0, 0)
	}
	result := connector.FetchResult{NextCursor: strconv.Itoa(page.Next), HasMore: page.HasNext}
	if len(page.Item) == 0 {
		if page.HasNext {
			return result, failure(connector.ErrInvalidRequest, 0, 0)
		}
		return result, nil
	}
	ids := make([]string, len(page.Item))
	for i, item := range page.Item {
		if item.ID.String() == "" {
			return result, failure(connector.ErrInvalidRequest, 0, 0)
		}
		ids[i] = item.ID.String()
	}
	var details struct {
		Items []json.RawMessage `json:"item_list"`
	}
	if err := p.get(ctx, cred, detailPath, url.Values{"item_id_list": {strings.Join(ids, ",")}}, &details); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, raw := range details.Items {
		var item struct {
			ID json.Number `json:"item_id"`
		}
		if json.Unmarshal(raw, &item) != nil || !containsID(ids, item.ID.String()) || seen[item.ID.String()] {
			return result, failure(connector.ErrInvalidRequest, 0, 0)
		}
		seen[item.ID.String()] = true
		result.Items = append(result.Items, connector.FetchItem{ExternalID: item.ID.String(), Payload: raw})
	}
	if len(seen) != len(ids) {
		return connector.FetchResult{}, failure(connector.ErrUnavailable, 0, 0)
	}
	return result, nil
}

func containsID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func (p *Platform) get(ctx context.Context, c credentials, path string, params url.Values, target any) error {
	timestamp := strconv.FormatInt(p.now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(c.key))
	_, err := mac.Write([]byte(c.partner + path + timestamp + c.token + c.shop))
	if err != nil {
		return failure(connector.ErrInvalidRequest, 0, 0)
	}
	params.Set("partner_id", c.partner)
	params.Set("timestamp", timestamp)
	params.Set("access_token", c.token)
	params.Set("shop_id", c.shop)
	params.Set("sign", hex.EncodeToString(mac.Sum(nil)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path+"?"+params.Encode(), nil)
	if err != nil {
		return failure(connector.ErrInvalidRequest, 0, 0)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return failure(connector.ErrUnavailable, 0, 0)
	} // URL-bearing transport errors are never exposed.
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseFailure(response, p.now())
	}
	var envelope struct {
		Error    string          `json:"error"`
		Response json.RawMessage `json:"response"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 || json.Unmarshal(data, &envelope) != nil {
		return failure(connector.ErrUnavailable, 200, 0)
	}
	switch envelope.Error {
	case "":
	case "error_auth", "error_token", "error_permission":
		return failure(connector.ErrAuthExpired, 200, 0)
	case "error_rate_limit", "error_too_many_requests":
		return failure(connector.ErrRateLimited, 200, 1)
	default:
		return failure(connector.ErrUnavailable, 200, 0)
	}
	if len(envelope.Response) == 0 || string(envelope.Response) == "null" || json.Unmarshal(envelope.Response, target) != nil {
		return failure(connector.ErrUnavailable, 200, 0)
	}
	return nil
}

func failure(kind connector.ErrKind, status, retry int) error {
	return &connector.PlatformError{Kind: kind, Status: status, RetryAfterSeconds: retry}
}
func responseFailure(response *http.Response, now time.Time) error {
	switch response.StatusCode {
	case 401, 403:
		return failure(connector.ErrAuthExpired, response.StatusCode, 0)
	case 429:
		retry, err := strconv.Atoi(response.Header.Get("Retry-After"))
		if err != nil {
			if date, e := http.ParseTime(response.Header.Get("Retry-After")); e == nil {
				retry = int(date.Sub(now).Seconds())
			}
		}
		return failure(connector.ErrRateLimited, 429, max(1, retry))
	default:
		kind := connector.ErrInvalidRequest
		if response.StatusCode >= 500 {
			kind = connector.ErrUnavailable
		}
		return failure(kind, response.StatusCode, 0)
	}
}
