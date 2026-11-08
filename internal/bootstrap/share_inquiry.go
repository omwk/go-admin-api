package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/omwk/go-admin-api/internal/modules/share"
	"gorm.io/gorm"
)

const inquiryMetadata = "i.id,i.tenant_id,i.share_link_id,i.customer_id,COALESCE(c.owner_user_id,i.owner_user_id) AS owner_user_id,i.status,i.created_at,i.updated_at,i.converted_at,i.pii_purged_at,i.revision,i.key_version"

func (s *shareStore) inquiryBase(ctx context.Context, tenant int64) (*gorm.DB, context.CancelFunc, error) {
	if _, err := scopeRegistryQuery("Share.Inquiries"); err != nil {
		return nil, func() {}, err
	}
	a, err := identityscope.From(ctx)
	if err != nil || a.Tenant != tenant {
		return nil, func() {}, identityscope.Denied()
	}
	q, cancel := s.database.WithContext(ctx)
	q = q.Table("inquiry i").Where("i.tenant_id=?", tenant).
		Joins("LEFT JOIN customer c ON c.tenant_id=i.tenant_id AND c.id=i.customer_id").Where("i.customer_id IS NULL OR c.id IS NOT NULL").
		Joins("LEFT JOIN sys_user scope_owner ON scope_owner.tenant_id=i.tenant_id AND scope_owner.id=COALESCE(c.owner_user_id,i.owner_user_id)")
	return shareOwnerScope(q, a), cancel, nil
}

func (s *shareStore) Inquiries(ctx context.Context, tenant int64, in share.Query) ([]share.Inquiry, int64, error) {
	q, cancel, err := s.inquiryBase(ctx, tenant)
	if err != nil {
		return nil, 0, err
	}
	defer cancel()
	if in.Status != "" {
		q = q.Where("i.status=?", in.Status)
	}
	if in.CustomerID > 0 {
		q = q.Where("i.customer_id=?", in.CustomerID)
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []share.Inquiry{}
	err = q.Select(inquiryMetadata).Order("i.created_at DESC,i.id DESC").Limit(in.PageSize).Offset((in.Page - 1) * in.PageSize).Find(&rows).Error
	return rows, total, err
}

func (s *shareStore) Inquiry(ctx context.Context, tenant, id int64, lock bool) (share.Inquiry, error) {
	q, cancel, err := s.inquiryBase(ctx, tenant)
	if err != nil {
		return share.Inquiry{}, err
	}
	defer cancel()
	var row share.Inquiry
	columns := inquiryMetadata + ",i.contact_name_cipher,i.contact_name_nonce,i.contact_phone_cipher,i.contact_phone_nonce,i.contact_email_cipher,i.contact_email_nonce,i.company_cipher,i.company_nonce,i.message_cipher,i.message_nonce,i.items_cipher,i.items_nonce"
	err = shareLock(q, "i", lock).Select(columns).Where("i.id=?", id).Take(&row).Error
	return row, shareDBError(err)
}

func (s *shareStore) UpdateInquiry(ctx context.Context, row share.Inquiry, status string, now time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	update := map[string]any{"status": status, "updated_at": now}
	if status == "won" && row.ConvertedAt == nil {
		update["converted_at"] = now
	}
	return shareChanged(q.Table("inquiry").Where("tenant_id=? AND id=? AND status=?", row.TenantID, row.ID, row.Status).Updates(update))
}

func (s *shareStore) Receipt(ctx context.Context, wanted share.InquiryReceipt, now time.Time) (share.InquiryReceipt, bool, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row share.InquiryReceipt
	r := q.Table("share_inquiry_receipt").Select("tenant_id,share_link_id,scope_hash,key_hash,request_hash,inquiry_id,expires_at").
		Where("tenant_id=? AND share_link_id=? AND scope_hash=? AND key_hash=? AND expires_at>?", wanted.TenantID, wanted.ShareLinkID, wanted.ScopeHash, wanted.KeyHash, now).Find(&row)
	return row, r.RowsAffected == 1, r.Error
}

func (s *shareStore) InsertInquiry(ctx context.Context, row *share.Inquiry, receipt *share.InquiryReceipt) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if row.CustomerID != nil {
		if err := q.Table("customer").Select("owner_user_id").Where("tenant_id=? AND id=? AND status='active' AND deleted_at IS NULL", row.TenantID, *row.CustomerID).Take(&row.OwnerUserID).Error; err != nil {
			return err
		}
	}
	if err := q.Create(row).Error; err != nil {
		return err
	}
	if err := q.Exec("DELETE FROM share_inquiry_receipt WHERE tenant_id=? AND share_link_id=? AND scope_hash=? AND key_hash=? AND expires_at<=?", receipt.TenantID, receipt.ShareLinkID, receipt.ScopeHash, receipt.KeyHash, row.CreatedAt).Error; err != nil {
		return err
	}
	if err := q.Create(receipt).Error; err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"inquiry_id": row.ID, "message": "有新的询价待处理"})
	if err != nil {
		return err
	}
	if err := q.Table("notification").Create(map[string]any{"tenant_id": row.TenantID, "channel": "inbox", "target": fmt.Sprintf("user:%d", row.OwnerUserID),
		"template": "share_inquiry_received", "payload": json.RawMessage(payload), "dedup_key": fmt.Sprintf("inquiry:%d", row.ID)}).Error; err != nil {
		return err
	}
	if err := outbox.Emit(ctx, s.database, outbox.Event{TenantID: row.TenantID, AggregateType: "inquiry", AggregateID: fmt.Sprint(row.ID), EventType: "share.inquiry.received", Payload: payload}); err != nil {
		return err
	}
	return s.Audit(ctx, audit.Event{TenantID: row.TenantID, ActorType: "system", Action: "share.inquiry.submit", ResourceType: "inquiry", ResourceID: fmt.Sprint(row.ID), Result: "success"})
}

var _ share.InquiryStore = (*shareStore)(nil)
