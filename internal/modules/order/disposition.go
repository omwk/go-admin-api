package order

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type safeEvidence struct {
	Source           Source      `json:"source"`
	Revision         *int64      `json:"revision"`
	States           States      `json:"candidate_states"`
	Money            Money       `json:"platform_amounts"`
	Correction       *Correction `json:"correction,omitempty"`
	CorrectionProved bool        `json:"correction_proved"`
}

func appendHistory(tx *gorm.DB, orderID int64, s Snapshot, changes []StateChange, kind, reason string, now time.Time) error {
	evidence := safeEvidence{Source: s.Source, Revision: s.Revision, States: s.States, Money: s.Money}
	if kind == "correction" {
		evidence.Correction = s.Correction
		reason = s.Correction.ReasonCode
	}
	evidence.CorrectionProved = s.Mode == ChangeCorrection && kind != "conflict"
	raw, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	for _, change := range changes {
		key, err := json.Marshal(struct {
			Evidence json.RawMessage
			StateChange
			Kind, Reason string
		}{raw, change, kind, reason})
		if err != nil {
			return err
		}
		digest := sha256.Sum256(key)
		row := StatusHistory{TenantID: s.TenantID, OrderID: orderID, Dimension: change.Dimension, FromStatus: change.From, ToStatus: change.To,
			EventKind: kind, Reason: reason, Evidence: raw, EventKey: hex.EncodeToString(digest[:]), OccurredAt: s.OccurredAt, ObservedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "order_id"}, {Name: "event_key"}}, DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func recordConflict(tx *gorm.DB, old Order, s Snapshot, decision StateDecision) (Result, error) {
	if old.ID == 0 {
		return quarantine(tx, old, s, Deltas{}, decision.Reason)
	}
	if len(decision.Changes) == 0 {
		decision.Changes = []StateChange{{"aggregate", "retained", "rejected"}}
	}
	if err := appendHistory(tx, old.ID, s, decision.Changes, "conflict", decision.Reason, time.Now().UTC()); err != nil {
		return Result{}, err
	}
	return Result{OrderID: old.ID, Version: old.Version, Outcome: "conflict", Reason: decision.Reason}, nil
}

func quarantine(tx *gorm.DB, old Order, s Snapshot, deltas Deltas, reason string) (Result, error) {
	detail, err := json.Marshal(struct {
		Reason string `json:"reason"`
		Source Source `json:"source"`
		Money  Money  `json:"platform_amounts"`
		Deltas Deltas `json:"deltas"`
	}{reason, s.Source, s.Money, deltas})
	if err != nil {
		return Result{}, err
	}
	row := map[string]any{"tenant_id": s.TenantID, "shop_id": s.ShopID, "resource_type": "order", "external_id": s.ExternalOrderID,
		"raw_id": s.Source.RawID, "reason_code": "invalid_value", "reason_detail": json.RawMessage(detail), "status": "pending"}
	if err := tx.Table("quarantine_record").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "raw_id"}, {Name: "reason_code"}}, DoNothing: true}).Create(row).Error; err != nil {
		return Result{}, err
	}
	return Result{OrderID: old.ID, Version: old.Version, Outcome: "quarantined", Reason: reason, Deltas: deltas}, nil
}
