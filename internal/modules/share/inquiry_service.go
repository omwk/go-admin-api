package share

import (
	"context"
	"encoding/csv"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// NewInquiryService requires live scope, authentication and audit persistence.
func NewInquiryService(store InquiryStore, sealer crypto.Sealer, recent RecentAuthentication, idempotency idem.Store, now func() time.Time) (*InquiryService, error) {
	if store == nil || recent == nil || idempotency == nil {
		return nil, errors.New("inquiry authorization dependencies required")
	}
	if now == nil {
		now = time.Now
	}
	return &InquiryService{store: store, sealer: sealer, recent: recent, now: now, idempotency: idempotency}, nil
}

// List returns only metadata and constant masks, never decrypting any field.
func (s *InquiryService) List(ctx context.Context, a Actor, q Query) (Page[InquiryView], error) {
	q, err := inquiryQuery(q)
	if err != nil {
		return Page[InquiryView]{}, err
	}
	ctx, err = s.store.Authorize(ctx, a, permission.CRMInquiryList)
	if err != nil {
		return Page[InquiryView]{}, err
	}
	rows, total, err := s.store.Inquiries(ctx, a.TenantID, q)
	result := Page[InquiryView]{List: []InquiryView{}, Total: total, Page: q.Page, PageSize: q.PageSize}
	for _, row := range rows {
		result.List = append(result.List, maskedInquiry(row))
	}
	return result, err
}

// Read discloses all fields only after current scope, recent authentication and durable audit.
func (s *InquiryService) Read(ctx context.Context, a Actor, id int64, sid string) (InquiryInput, error) {
	authorized, err := s.store.Authorize(ctx, a, permission.TradeInquiryPIIRead)
	if err == nil {
		err = s.recent(authorized, a.TenantID, a.UserID, sid)
	}
	var out InquiryInput
	if err == nil {
		err = s.store.Transaction(authorized, func(tx context.Context) error {
			row, err := s.store.Inquiry(tx, a.TenantID, id, true)
			if err != nil {
				return err
			}
			out, err = openInquiry(s.sealer, row)
			if err != nil {
				return err
			}
			return s.store.Audit(tx, inquiryReadAudit(a, id, "success"))
		})
	}
	if err != nil {
		if auditErr := s.store.Audit(ctx, inquiryReadAudit(a, id, "failure")); auditErr != nil {
			return InquiryInput{}, unavailable()
		}
		return InquiryInput{}, err
	}
	return out, nil
}

func inquiryReadAudit(a Actor, id int64, result string) audit.Event {
	return audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "share.inquiry.pii.read", ResourceType: "inquiry", ResourceID: strconv.FormatInt(id, 10),
		After: map[string]any{"fields": inquiryFieldNames, "purpose": "inquiry_followup"}, Result: result}
}

// Update changes the workflow while preserving the first conversion's retention deadline.
func (s *InquiryService) Update(ctx context.Context, a Actor, id int64, status string, op *idem.Operation) error {
	if !inquiryStatus(status) {
		return Invalid()
	}
	ctx, err := s.store.Authorize(ctx, a, permission.CRMInquiryUpdate)
	if err != nil {
		return err
	}
	_, err = mutate(ctx, s.store, s.idempotency, a, op, func(tx context.Context, _ idem.Result) error {
		_, err := s.store.Inquiry(tx, a.TenantID, id, false)
		return err
	}, func(tx context.Context) (idem.Result, error) {
		err := s.updateInquiry(tx, a, id, status)
		return idem.Result{Status: 200, Body: []byte(`{"updated":true}`)}, err
	})
	return err
}

func (s *InquiryService) updateInquiry(tx context.Context, a Actor, id int64, status string) error {
	row, err := s.store.Inquiry(tx, a.TenantID, id, true)
	if err != nil {
		return err
	}
	if row.Status == status {
		return nil
	}
	rank := map[string]int{"new": 0, "contacted": 1, "quoted": 2, "won": 3, "lost": 3}
	if rank[status] < rank[row.Status] || rank[row.Status] == 3 {
		return errs.New(errs.ErrConflict, 409, "询价状态不可变更")
	}
	if err := s.store.UpdateInquiry(tx, row, status, s.now().UTC()); err != nil {
		return err
	}
	return s.store.Audit(tx, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "share.inquiry.update", ResourceType: "inquiry", ResourceID: strconv.FormatInt(id, 10), After: map[string]any{"status": status}, Result: "success"})
}

// CSV produces one bounded page using a fixed non-PII column whitelist.
func (s *InquiryService) CSV(ctx context.Context, a Actor, q Query) (string, error) {
	ctx, err := s.store.Authorize(ctx, a, permission.TradeInquiryExport)
	if err != nil {
		return "", err
	}
	q, err = inquiryQuery(q)
	if err != nil {
		return "", err
	}
	rows, _, err := s.store.Inquiries(ctx, a.TenantID, q)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	w := csv.NewWriter(&out)
	if err := w.Write([]string{"id", "share_link_id", "status", "created_at", "converted_at", "pii_purged_at"}); err != nil {
		return "", err
	}
	for _, row := range rows {
		if err := w.Write([]string{strconv.FormatInt(row.ID, 10), strconv.FormatInt(row.ShareLinkID, 10), row.Status, row.CreatedAt.UTC().Format(time.RFC3339), csvTime(row.ConvertedAt), csvTime(row.PIIPurgedAt)}); err != nil {
			return "", err
		}
	}
	w.Flush()
	return out.String(), w.Error()
}

func csvTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func maskedInquiry(row Inquiry) InquiryView {
	return InquiryView{ID: row.ID, ShareLinkID: row.ShareLinkID, CustomerID: row.CustomerID, OwnerUserID: row.OwnerUserID, Status: row.Status, CreatedAt: row.CreatedAt,
		ConvertedAt: row.ConvertedAt, PIIPurgedAt: row.PIIPurgedAt, ContactName: "*", ContactPhone: "***********", ContactEmail: "***", Company: "*", Message: "*"}
}

func inquiryStatus(status string) bool {
	return status == "new" || status == "contacted" || status == "quoted" || status == "won" || status == "lost"
}

func inquiryQuery(q Query) (Query, error) {
	if q.Page == 0 {
		q.Page = 1
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.Page < 1 || q.Page > 500 || q.PageSize < 1 || q.PageSize > 100 || q.Keyword != "" || q.Type != "" || q.Status != "" && !inquiryStatus(q.Status) || q.Sort != "" && q.Sort != "-created_at" {
		return Query{}, Invalid()
	}
	return q, nil
}
