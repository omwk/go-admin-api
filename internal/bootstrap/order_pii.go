package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/order"
	"gorm.io/gorm"
)

type orderPIIStore struct{ *orderQueryStore }

// NewOrderPIIStore binds address access to the same live shop scope as order queries.
func NewOrderPIIStore(database *db.DB) order.PIIStore {
	return &orderPIIStore{orderQueryStore: &orderQueryStore{database: database}}
}

func (s *orderPIIStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return s.database.Transaction(ctx, fn)
}

func (s *orderPIIStore) Address(ctx context.Context, tenant, id int64) (order.Address, error) {
	base, cancel, err := s.base(ctx, tenant, "Order.PIIRead")
	if err != nil {
		return order.Address{}, err
	}
	defer cancel()
	var row order.Address
	err = base.Joins("JOIN order_address a ON a.tenant_id=o.tenant_id AND a.order_id=o.id").
		Select("a.tenant_id,a.order_id,a.receiver_name_cipher,a.receiver_name_nonce,a.phone_cipher,a.phone_nonce,a.address_cipher,a.address_nonce,a.key_version,a.revision").
		Where("o.id=?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return order.Address{}, order.NotFound()
	}
	return row, err
}

func (s *orderPIIStore) Audit(ctx context.Context, event audit.Event) error {
	return audit.Record(ctx, s.database, event)
}

func (s *orderQueryStore) maskedAddresses(ctx context.Context, tenant int64, ids []int64) (map[int64]order.AddressView, error) {
	result := make(map[int64]order.AddressView, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var rows []order.Address
	err := q.Select("order_id,receiver_name_mask,phone_mask").Where("tenant_id=? AND order_id IN ?", tenant, ids).Find(&rows).Error
	for _, row := range rows {
		result[row.OrderID] = order.MaskedAddress(row.ReceiverNameMask, row.PhoneMask)
	}
	return result, err
}
