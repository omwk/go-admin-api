package billing

import "time"

type unitView struct {
	ID             int64     `json:"id"`
	EntitlementID  int64     `json:"entitlement_id"`
	CallID         string    `json:"call_id"`
	Type           string    `json:"type"`
	Amount         int64     `json:"amount"`
	RemainingAfter int64     `json:"remaining_after"`
	ReservedAfter  int64     `json:"reserved_after"`
	ServiceDate    string    `json:"service_date"`
	PostingDate    string    `json:"posting_date"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func viewUnit(u UnitTxn) unitView {
	return unitView{u.ID, u.EntitlementID, u.CallID, u.Type, u.Amount, u.RemainingAfter, u.ReservedAfter, u.ServiceDate.UTC().Format("2006-01-02"), u.OccurredAt.UTC().Format("2006-01-02"), u.OccurredAt}
}
