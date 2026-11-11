package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/db"
)

// Service queues safe requests and performs delivery outside transaction boundaries.
type Service struct {
	Store   Store
	Sealer  crypto.Sealer
	Now     func() time.Time
	Factory func(string, string, Config) (Sender, error)
	// Prepare validates business message credentials before rendering or sending.
	Prepare func(context.Context, int64, Request) (Request, error)
}

// NewService composes a delivery service with injectable transport construction.
func NewService(store Store, sealer crypto.Sealer, now func() time.Time) *Service {
	return &Service{Store: store, Sealer: sealer, Now: now, Factory: NewSender}
}

// Enqueue participates in the caller's transaction and never performs external I/O.
func (s *Service) Enqueue(ctx context.Context, tenant int64, r Request) (Delivery, error) {
	var d Delivery
	if tenant <= 0 || !validChannel(r.Channel) || r.TemplateCode == "" || r.UserID <= 0 || len(r.Variables) > 32 {
		return d, errors.New("invalid notification request")
	}
	if r.Locale == "" {
		r.Locale = "zh-CN"
	}
	err := s.Store.Transaction(ctx, func(tx context.Context) error {
		d = Delivery{TenantID: tenant, Channel: r.Channel, TemplateCode: r.TemplateCode, Status: "pending", CreatedAt: s.Now().UTC()}
		o := Outbox{TenantID: tenant, Channel: r.Channel, TemplateCode: r.TemplateCode, Status: "pending"}
		if err := s.Store.Insert(tx, &d, &o); err != nil {
			return err
		}
		payload, err := s.requestPayload(tx, tenant, d.ID, r)
		if err != nil {
			return err
		}
		o.Payload = payload
		return s.Store.Insert(tx, &d, &o)
	})
	return d, err
}

// Send queues then delivers; an unavailable or failed provider remains a delivery result.
func (s *Service) Send(ctx context.Context, tenant int64, r Request) (Delivery, error) {
	d, err := s.Enqueue(ctx, tenant, r)
	if err != nil || db.InTransaction(ctx) {
		return d, err
	}
	return s.Deliver(ctx, tenant, d.ID)
}

// Deliver leases one outbox entry and records a safe final result.
func (s *Service) Deliver(ctx context.Context, tenant, id int64) (Delivery, error) {
	d, o, found, err := s.Store.Claim(ctx, tenant, id, s.Now().UTC())
	if err != nil || !found {
		return d, err
	}
	lease := *d.NextRetryAt
	var r Request
	if s.openRequest(d, o, &r) != nil {
		d.Status, d.LastError = "failed", "payload_unavailable"
		return d, s.Store.Finish(ctx, d, lease, "failed", false)
	}
	status, alert, err := s.dispatch(ctx, &d, r)
	if err != nil {
		return d, err
	}
	err = s.Store.Finish(ctx, d, lease, status, alert)
	d.NextRetryAt = nil
	if status == "channel_unavailable" {
		at := lease.Add(2 * time.Minute)
		d.NextRetryAt = &at
	}
	return d, err
}

func (s *Service) requestPayload(ctx context.Context, tenant, id int64, r Request) (json.RawMessage, error) {
	if s.Sealer != nil {
		return SealJSON(s.Sealer, outboxAAD(tenant, id), r)
	}
	// Keyless operation is only for unavailable channels; never retain private inputs.
	// This mode still preserves safe template inputs for delivery after configuration.
	for _, kind := range []string{"email", "sms"} {
		recipient, err := s.Store.Recipient(ctx, tenant, r.UserID, kind)
		if err != nil {
			continue
		}
		for key, value := range r.Variables {
			if recipient != "" && strings.Contains(value, recipient) {
				delete(r.Variables, key)
			}
		}
	}
	return json.Marshal(struct {
		Reference Request `json:"reference"`
	}{r})
}

func (s *Service) openRequest(d Delivery, o Outbox, r *Request) error {
	var ref struct {
		Reference *Request `json:"reference"`
	}
	if json.Unmarshal(o.Payload, &ref) == nil && ref.Reference != nil {
		*r = *ref.Reference
		return nil
	}
	return OpenJSON(s.Sealer, outboxAAD(d.TenantID, d.ID), o.Payload, r)
}

func (s *Service) dispatch(ctx context.Context, d *Delivery, r Request) (string, bool, error) {
	providers, err := s.Store.Providers(ctx, d.TenantID, d.Channel)
	if err != nil {
		return "", false, err
	}
	providers = selectProviders(providers, r.ProviderID)
	if len(providers) == 0 {
		d.Status, d.LastError = "skipped", "channel_unavailable"
		return "channel_unavailable", false, nil
	}
	if s.Prepare != nil {
		r, err = s.Prepare(ctx, d.TenantID, r)
		if err != nil {
			d.Status, d.LastError = "failed", "message_unavailable"
			return "failed", false, nil
		}
	}
	t, err := s.Store.Template(ctx, d.TenantID, r.TemplateCode, r.Locale)
	if err != nil {
		return "", false, err
	}
	m, err := Render(t, r.Variables)
	if err != nil {
		d.Status, d.LastError = "failed", "template_render_failed"
		return "failed", false, nil
	}
	recipient, err := s.Store.Recipient(ctx, d.TenantID, r.UserID, r.Channel)
	if err != nil {
		d.Status, d.LastError = "failed", "recipient_unavailable"
		return "failed", false, nil
	}
	d.RecipientHash, d.RecipientDomain = recipientFingerprint(recipient)
	m.ID, m.Recipient = d.ID, recipient
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, p := range providers {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		d.Attempts++
		d.ProviderID = &p.ID
		if s.send(ctx, p, m, r.Test) == nil {
			now := s.Now().UTC()
			d.Status, d.LastError, d.SentAt = "sent", "", &now
			return "sent", false, nil
		}
		d.Status, d.LastError = "failed", "provider_delivery_failed"
	}
	return "failed", true, nil
}

func (s *Service) send(ctx context.Context, p Provider, m Message, test bool) error {
	var cfg Config
	if err := OpenJSON(s.Sealer, ProviderAAD(p), p.Config, &cfg); err != nil {
		return err
	}
	defer clear(cfg)
	cfg["test_recipient"] = m.Recipient
	sender, err := s.Factory(p.Channel, p.Kind, cfg)
	if err != nil {
		return err
	}
	if test {
		return sender.Test(ctx)
	}
	return sender.Send(ctx, m)
}

func validChannel(c string) bool {
	switch c {
	case "email", "sms", "wecom", "dingtalk", "webhook":
		return true
	}
	return false
}

func selectProviders(providers []Provider, id int64) []Provider {
	selected := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if p.Enabled && (id == 0 || p.ID == id) {
			selected = append(selected, p)
		}
	}
	return selected
}

func recipientFingerprint(recipient string) (string, string) {
	if recipient == "" {
		return "", ""
	}
	sum := sha256.Sum256([]byte(recipient))
	domain := ""
	if a, err := mail.ParseAddress(recipient); err == nil && a.Address == recipient {
		_, domain, _ = strings.Cut(recipient, "@")
		domain = strings.ToLower(domain)
	}
	return hex.EncodeToString(sum[:]), domain
}
