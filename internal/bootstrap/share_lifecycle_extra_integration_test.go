package bootstrap

import (
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestShareLifecycleIntegrationImplicitSourceAndReactivation(t *testing.T) {
	f := newShareAccessFixture(t)
	list := f.list(t, nil)
	f.batch(t, &list, share.ItemInput{SPUID: f.spu, PriceCents: 300})
	link := f.issue(t, f.publication())
	root := f.anonymous(t, "GET", link.URL, "192.0.2.61", nil, nil)
	require.Equal(t, 200, root.Code)
	input := share.ListInput{Type: list.Type, Name: list.Name, Currency: list.Currency, Status: "disabled", Version: list.Version}
	r, _ := f.request(t, "PUT", fmt.Sprintf("/price-lists/%d", list.ID), input)
	require.Equal(t, 200, r.Code, r.Body.String())
	input.Status, input.Version = "active", input.Version+1
	r, _ = f.request(t, "PUT", fmt.Sprintf("/price-lists/%d", list.ID), input)
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Equal(t, 404, f.anonymous(t, "GET", link.URL, "192.0.2.61", nil, shareCookie(t, root)).Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var revoked int64
	require.NoError(t, q.Table("share_link").Where("id=? AND status='revoked'", link.ID).Count(&revoked).Error)
	require.EqualValues(t, 1, revoked)
	t.Log("implicit frozen price source disabled=revoked; parent reactivation does not revive cached link")
}

func TestShareInquiryIntegrationBoundCustomerTransferUsesCurrentOwner(t *testing.T) {
	f := newShareAccessFixture(t)
	customer := f.customer(t, "current-owner")
	in := f.publication()
	in.CustomerID, in.Password = customer.ID, "Synthetic-Quote-Gate"
	link := f.issue(t, in)
	verify := f.anonymous(t, "POST", link.URL+"/verify", "192.0.2.62", map[string]any{"password": in.Password}, nil)
	root := f.anonymous(t, "GET", link.URL, "192.0.2.62", nil, shareCookie(t, verify))
	f.now = f.now.Add(3 * time.Second)
	r := inquiryRequest(t, f, link, shareCookie(t, root), "192.0.2.62", "owner", "https://quote.test", share.InquiryInput{ContactName: "synthetic", ContactPhone: "synthetic"})
	require.Equal(t, 201, r.Code, r.Body.String())
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("inquiry").Select("id").Take(&id).Error)
	r, _ = f.request(t, "PUT", fmt.Sprintf("/customers/%d", customer.ID), share.CustomerInput{Code: customer.Code, Name: customer.Name, OwnerUserID: f.user.ID, Status: "active", Version: customer.Version})
	require.Equal(t, 200, r.Code, r.Body.String())
	ordinaryScopeActor(t, f.identityFixture, f.actor.UserID)
	require.NoError(t, q.Table("sys_role").Where("tenant_id=1").Update("data_scope", "self").Error)
	reauthInquiry(t, f)
	require.Equal(t, 404, inquiryPII(t, f, id).Code)
	r, _ = f.request(t, "GET", "/inquiries", nil)
	require.Contains(t, r.Body.String(), `"total":0`)
	t.Log("bound inquiry follows current customer owner after transfer; former owner loses rows/count/disclosure immediately")
}
