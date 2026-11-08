package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

type inquiryDiagnosticStore struct {
	*shareStore
	failure error
}

func (s *inquiryDiagnosticStore) InsertInquiry(ctx context.Context, row *share.Inquiry, receipt *share.InquiryReceipt) error {
	err := s.shareStore.InsertInquiry(ctx, row, receipt)
	s.failure = err
	return err
}

func TestShareInquiryIntegrationAdmittedSubmission(t *testing.T) {
	// REQ-SHR-008: anonymous intake returns only a safe receipt.
	f := newShareAccessFixture(t)
	link := f.issue(t, f.publication())
	root := f.anonymous(t, "GET", link.URL, "192.0.2.50", nil, nil)
	cookie := shareCookie(t, root)
	f.now = f.now.Add(3 * time.Second)
	probe := &inquiryDiagnosticStore{shareStore: &shareStore{database: f.database}}
	svc, err := share.NewLinkService(share.LinkDependencies{Store: probe, Idempotency: newIdempotencyStore(f.database), Sealer: f.sealer, Cache: f.redis.cache, Now: func() time.Time { return f.now }})
	require.NoError(t, err)
	err = svc.SubmitInquiry(f.ctx, 1, shareToken(link), "192.0.2.50", cookie.Value, "direct", share.InquiryInput{ContactName: "INQUIRY-CANARY", ContactPhone: "PHONE-CANARY"})
	require.NoError(t, probe.failure)
	require.NoError(t, err)
	r := f.anonymous(t, "POST", link.URL+"/inquiry", "192.0.2.51", map[string]any{"contact_name": "INQUIRY-CANARY", "contact_phone": "PHONE-CANARY", "company": "COMPANY-CANARY", "message": "MESSAGE-CANARY"}, cookie)
	require.Equal(t, 201, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"submitted":true`)
	require.NotContains(t, r.Body.String(), "CANARY")
}
