package share

import (
	"context"
	"strconv"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// Visits requires its own capability, live owner scope, step-up and a durable audit.
func (s *InquiryService) Visits(ctx context.Context, a Actor, link int64, sid string, q Query) (Page[Visit], error) {
	q, err := inquiryQuery(q)
	if err != nil {
		return Page[Visit]{}, err
	}
	authorized, err := s.store.Authorize(ctx, a, permission.ShareVisitList)
	if err == nil {
		err = s.recent(authorized, a.TenantID, a.UserID, sid)
	}
	event := audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "share.visit.read", ResourceType: "share_link", ResourceID: strconv.FormatInt(link, 10), Result: "success"}
	var out Page[Visit]
	if err == nil {
		err = s.store.Transaction(authorized, func(tx context.Context) error {
			rows, total, err := s.store.Visits(tx, a.TenantID, link, q)
			if err != nil {
				return err
			}
			out = Page[Visit]{List: rows, Total: total, Page: q.Page, PageSize: q.PageSize}
			return s.store.Audit(tx, event)
		})
	}
	if err != nil {
		event.Result = "failure"
		if auditErr := s.store.Audit(ctx, event); auditErr != nil {
			return Page[Visit]{}, unavailable()
		}
		return Page[Visit]{}, err
	}
	return out, nil
}
