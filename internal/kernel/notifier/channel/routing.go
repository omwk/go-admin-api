package channel

import (
	"context"
	"errors"
	"strings"
)

func personalRequest(r Request) bool {
	return r.Target == "" || strings.HasPrefix(r.Target, "user:")
}

func (s *Service) route(ctx context.Context, tenant int64, r Request) (Request, error) {
	if s.Route == nil || r.Test || !personalRequest(r) {
		return r, nil
	}
	return s.Route(ctx, tenant, r)
}

func (s *Service) dispatchPersonal(ctx context.Context, d *Delivery, r Request, m Message) (string, bool, error) {
	d.Attempts++
	d.ProviderID = nil
	if err := s.Personal.Send(ctx, d.Channel, m.Recipient, m); err != nil {
		d.Status, d.LastError = "failed", "provider_delivery_failed"
		return "failed", true, nil
	}
	if s.PersonalSent != nil && r.Test {
		if err := s.PersonalSent(ctx, d.TenantID, r); err != nil {
			if errors.Is(err, ErrPersonalDestinationChanged) {
				d.Status, d.LastError = "failed", "recipient_unavailable"
				return "failed", false, nil
			}
			return "", false, err
		}
	}
	now := s.Now().UTC()
	d.Status, d.LastError, d.SentAt = "sent", "", &now
	return "sent", false, nil
}
