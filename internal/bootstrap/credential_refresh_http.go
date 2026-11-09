package bootstrap

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
)

func (r *credentialRefreshRuntime) scopedCredential(ctx context.Context, actor shop.Actor, id int64, code string) error {
	store := newSyncEngineStore(r.database, nil, config.Sync{})
	human, err := store.Authorize(ctx, actor.TenantID, actor.UserID, code)
	if err != nil {
		return err
	}
	return r.database.Transaction(human, func(tx context.Context) error {
		_, err := newShopStore(r.database).Credential(tx, actor.TenantID, id, model.CredentialAudit{TenantID: actor.TenantID, ActorID: &actor.UserID, ActorType: "user", Action: "read", Result: "success", Detail: json.RawMessage(`{"metadata_only":true}`), TraceID: "refresh-history"})
		return err
	})
}

func (r *credentialRefreshRuntime) Submit(ctx context.Context, actor shop.Actor, id int64, operation *idem.Operation) (shop.RefreshAccepted, error) {
	var result shop.RefreshAccepted
	if err := r.scopedCredential(ctx, actor, id, permission.ShopCredentialRotate); err != nil {
		return result, err
	}
	err := r.database.Transaction(ctx, func(tx context.Context) error {
		if operation != nil {
			claim, err := newIdempotencyStore(r.database).Claim(tx, *operation)
			if err != nil {
				return err
			}
			if claim.Action == idem.Replay {
				return json.Unmarshal(claim.Result.Body, &result)
			}
		}
		run, err := r.prepare(tx, actor.TenantID, id, "manual", &actor.UserID)
		if err != nil {
			return err
		}
		result = shop.RefreshAccepted{RunID: run.ID, ProtocolStatus: "verified"}
		if operation == nil {
			return nil
		}
		stored, err := idem.JSONResult(202, result)
		if err != nil {
			return err
		}
		_, err = newIdempotencyStore(r.database).Complete(tx, *operation, stored)
		return err
	})
	return result, err
}

func (r *credentialRefreshRuntime) History(ctx context.Context, actor shop.Actor, id int64, page, size int) (any, error) {
	if err := r.scopedCredential(ctx, actor, id, permission.ShopCredentialAudit); err != nil {
		return nil, err
	}
	q, cancel := r.database.WithContext(ctx)
	defer cancel()
	query := q.Table("credential_refresh_run").Where("tenant_id=? AND credential_id=?", actor.TenantID, id)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, credentialDBError(err)
	}
	rows := []shop.RefreshHistory{}
	err := query.Select("id,phase,status,trigger,attempt,failure_class,started_at,next_retry_at,output_version_id").Order("id DESC").Limit(size).Offset((page - 1) * size).Find(&rows).Error
	return struct {
		List     []shop.RefreshHistory `json:"list"`
		Total    int64                 `json:"total"`
		Page     int                   `json:"page"`
		PageSize int                   `json:"page_size"`
	}{rows, total, page, size}, credentialDBError(err)
}
