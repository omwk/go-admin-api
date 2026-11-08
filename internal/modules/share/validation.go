package share

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// Invalid hides all rejected input values, including private fields.
func Invalid() error { return errs.New(errs.ErrInvalidParam, 400, "客户或价目表参数无效") }

// NotFound deliberately hides absent and inaccessible resource identities.
func NotFound() error { return errs.New(errs.ErrNotFound, 404, "客户或价目表不存在") }

// VersionConflict reports a stale optimistic update without writing anything.
func VersionConflict() error {
	return errs.New(errs.ErrVersionConflict, 409, "客户或价目表版本冲突")
}

func validText(s string, limit int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit
}

func normalizeCustomer(in CustomerInput, user int64) (CustomerInput, error) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if in.OwnerUserID == 0 {
		in.OwnerUserID = user
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	if in.Code == "" || in.Name == "" || !validText(in.Code, 100) || !validText(in.Name, 200) ||
		!validText(in.ContactName, 200) || !validText(in.Phone, 100) || !validText(in.Email, 320) ||
		(in.Status != "active" && in.Status != "disabled") || in.OwnerUserID <= 0 || !validText(in.Source, 100) {
		return in, Invalid()
	}
	return in, nil
}

func normalizeList(in ListInput) (ListInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Status == "" {
		in.Status = "draft"
	}
	if in.Type == "customer" {
		if in.CustomerID == nil || *in.CustomerID <= 0 {
			return in, Invalid()
		}
	} else if in.Type != "default" || in.CustomerID != nil {
		return in, Invalid()
	}
	if in.Name == "" || !validText(in.Name, 200) || !validCurrency(in.Currency) || !validText(in.Note, 2000) ||
		!map[string]bool{"draft": true, "active": true, "expired": true, "disabled": true}[in.Status] ||
		(in.ValidFrom != nil && in.ValidTo != nil && !in.ValidFrom.Before(*in.ValidTo)) || !utcTime(in.ValidFrom) || !utcTime(in.ValidTo) {
		return in, Invalid()
	}
	return in, nil
}

func validCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func utcTime(t *time.Time) bool {
	if t == nil {
		return true
	}
	_, offset := t.Zone()
	return !t.IsZero() && offset == 0
}

// NormalizeQuery restricts pagination and sort before any database operation.
func NormalizeQuery(q Query) (Query, error) {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Sort == "" {
		q.Sort = "-created_at"
	}
	if q.Page < 1 || q.PageSize < 1 || q.PageSize > 100 || q.Page > 500 || q.CustomerID < 0 || !validText(q.Keyword, 200) ||
		(q.Page-1)*q.PageSize >= 10000 || !map[string]bool{"-created_at": true, "created_at": true, "name": true, "-name": true}[q.Sort] ||
		(q.Type != "" && q.Type != "customer" && q.Type != "default") || (q.Status != "" && !map[string]bool{"draft": true, "active": true, "disabled": true, "expired": true}[q.Status]) {
		return q, Invalid()
	}
	return q, nil
}

func normalizeBatch(in BatchInput) (BatchInput, error) {
	if in.Version <= 0 || len(in.Items) == 0 || len(in.Items) > 500 {
		return in, Invalid()
	}
	seen := map[[2]int64]bool{}
	for i, item := range in.Items {
		key := [2]int64{item.SPUID, item.SKUID}
		if item.SPUID <= 0 || item.SKUID < 0 || seen[key] || item.PriceCents < 0 || !validText(item.Note, 2000) || item.MOQ < 0 || item.MOQ > 2147483647 {
			return in, Invalid()
		}
		seen[key] = true
		if item.MOQ == 0 {
			in.Items[i].MOQ = 1
		}
	}
	return in, nil
}
