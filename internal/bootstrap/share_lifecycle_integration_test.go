package bootstrap

import (
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/share"
	"github.com/stretchr/testify/require"
)

func TestShareLifecycleIntegrationCustomerAndQuoteRevocation(t *testing.T) {
	for _, scenario := range []string{"customer_disabled", "customer_deleted", "customer_transferred", "quote_disabled", "quote_deleted"} {
		t.Run(scenario, func(t *testing.T) {
			f := newShareAccessFixture(t)
			customer := f.customer(t, "lifecycle")
			list := f.list(t, &customer.ID)
			f.batch(t, &list, share.ItemInput{SPUID: f.spu, PriceCents: 500})
			in := f.publication()
			in.CustomerID, in.PriceListID, in.Password = customer.ID, list.ID, "Synthetic-Quote-Gate"
			link := f.issue(t, in)
			other := f.issue(t, f.publication())
			verify := f.anonymous(t, "POST", link.URL+"/verify", "192.0.2.50", map[string]any{"password": in.Password}, nil)
			cookie := shareCookie(t, verify)
			require.Equal(t, 200, f.anonymous(t, "GET", link.URL, "192.0.2.50", nil, cookie).Code)
			if scenario == "customer_deleted" {
				r, _ := f.request(t, "DELETE", fmt.Sprintf("/customers/%d?version=%d", customer.ID, customer.Version), nil)
				require.Equal(t, 204, r.Code, r.Body.String())
			} else if scenario == "customer_disabled" || scenario == "customer_transferred" {
				owner, status := f.actor.UserID, "disabled"
				if scenario == "customer_transferred" {
					owner, status = f.user.ID, "active"
				}
				r, _ := f.request(t, "PUT", fmt.Sprintf("/customers/%d", customer.ID), share.CustomerInput{Code: customer.Code, Name: customer.Name, OwnerUserID: owner, Status: status, Version: customer.Version})
				require.Equal(t, 200, r.Code, r.Body.String())
			} else if scenario == "quote_deleted" {
				r, _ := f.request(t, "DELETE", fmt.Sprintf("/price-lists/%d?version=%d", list.ID, list.Version), nil)
				require.Equal(t, 204, r.Code, r.Body.String())
			} else {
				r, _ := f.request(t, "PUT", fmt.Sprintf("/price-lists/%d", list.ID), share.ListInput{Type: list.Type, CustomerID: list.CustomerID, Name: list.Name, Currency: list.Currency, Status: "disabled", Version: list.Version})
				require.Equal(t, 200, r.Code, r.Body.String())
			}
			require.Equal(t, 404, f.anonymous(t, "GET", link.URL, "192.0.2.50", nil, cookie).Code)
			require.Equal(t, 200, f.anonymous(t, "GET", other.URL, "192.0.2.51", nil, nil).Code)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var status string
			require.NoError(t, q.Table("share_link").Select("status").Where("id=?", link.ID).Take(&status).Error)
			require.Equal(t, "revoked", status)
			var audits []string
			require.NoError(t, q.Raw("SELECT row_to_json(a)::text FROM audit_log a WHERE action='share.link.lifecycle.revoke'").Scan(&audits).Error)
			require.Len(t, audits, 1)
			require.Contains(t, audits[0], "actor_id")
			require.Contains(t, audits[0], "revoked_at")
			f.noLeaks(t, "lifecycle audit", audits[0])
			t.Log(scenario + ": cached link=404/revoked, unrelated link=200, safe audit persisted")
		})
	}
}

func TestShareLifecycleIntegrationAuditFailureRollsBack(t *testing.T) {
	f := newShareAccessFixture(t)
	customer := f.customer(t, "rollback")
	in := f.publication()
	in.CustomerID, in.Password = customer.ID, "Synthetic-Quote-Gate"
	link := f.issue(t, in)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_lifecycle_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='share.link.lifecycle.revoke' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_lifecycle_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_lifecycle_audit()`))
	r, _ := f.request(t, "DELETE", fmt.Sprintf("/customers/%d?version=%d", customer.ID, customer.Version), nil)
	require.Equal(t, 500, r.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"customer", "share_link"} {
		var status string
		id := customer.ID
		if table == "share_link" {
			id = link.ID
		}
		require.NoError(t, q.Table(table).Select("status").Where("id=?", id).Take(&status).Error)
		require.Equal(t, "active", status)
	}
	t.Log("audit failure=500; customer deletion and share revocation both rolled back")
}
