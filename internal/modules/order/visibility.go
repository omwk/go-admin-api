package order

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"time"
)

func changeVisibility(ctx context.Context, tx *gorm.DB, s Snapshot, vanished bool, reason string) error {
	if err := validateIdentity(s); err != nil {
		return err
	}
	tx = tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx)
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return errors.New("order transaction is required")
	}
	if err := verifyRelationships(tx, s); err != nil {
		return err
	}
	old, err := loadOrder(tx, s)
	if err != nil || old.ID == 0 {
		return err
	}
	if vanished == (old.VanishedAt != nil) {
		return nil
	}
	now := time.Now().UTC()
	from, to := "active", "vanished"
	var vanishedAt *time.Time
	var vanishReason *string
	if vanished {
		vanishedAt, vanishReason = &now, &reason
	} else {
		from, to, reason = "vanished", "active", "observed_again"
	}
	updated := tx.Model(&Order{}).Where("tenant_id=? AND id=? AND version=?", s.TenantID, old.ID, old.Version).
		Updates(map[string]any{"vanished_at": vanishedAt, "vanish_reason": vanishReason, "version": old.Version + 1, "updated_at": now})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return versionConflict()
	}
	// The transition version distinguishes re-observations without altering raw provenance.
	evidence, err := json.Marshal(struct {
		Source  Source `json:"source"`
		Version int64  `json:"visibility_version"`
	}{s.Source, old.Version + 1})
	if err != nil {
		return err
	}
	return appendHistoryEvidence(tx, old.ID, s, []StateChange{{"visibility", from, to}}, "snapshot", reason, now, evidence)
}
