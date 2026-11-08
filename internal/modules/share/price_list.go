package share

import "context"

// PriceLists returns the visible customer and creator-scoped price sources.
func (s *Service) PriceLists(ctx context.Context, a Actor, q Query) (Page[PriceList], error) {
	q, err := NormalizeQuery(q)
	if err != nil {
		return Page[PriceList]{}, err
	}
	rows, total, err := s.store.PriceLists(ctx, a.TenantID, q)
	return Page[PriceList]{List: rows, Total: total, Page: q.Page, PageSize: q.PageSize}, err
}

// PriceList reads one price source and its private-field-free item collection.
func (s *Service) PriceList(ctx context.Context, a Actor, id int64) (ListDetail, error) {
	var out ListDetail
	err := s.store.Snapshot(ctx, func(tx context.Context) error {
		row, err := s.store.PriceList(tx, a.TenantID, id, false)
		if err != nil {
			return err
		}
		items, err := s.store.Items(tx, a.TenantID, id)
		out = ListDetail{PriceList: row, Items: items}
		return err
	})
	return out, err
}

// SavePriceList writes explicit ownership and sealed metadata under an optimistic lock.
func (s *Service) SavePriceList(ctx context.Context, a Actor, id int64, in ListInput) (PriceList, error) {
	in, err := normalizeList(in)
	if err != nil {
		return PriceList{}, err
	}
	creating := id == 0
	if !creating && in.Version <= 0 || creating && in.Version != 0 {
		return PriceList{}, Invalid()
	}
	var out PriceList
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		creator := a.UserID
		revision := 1
		if !creating {
			old, err := s.store.PriceList(tx, a.TenantID, id, true)
			if err != nil {
				return err
			}
			if old.Version != in.Version {
				return VersionConflict()
			}
			creator = old.CreatedBy
			revision = old.Revision + 1
		} else {
			var err error
			id, err = s.store.NextID(tx, "price_list")
			if err != nil {
				return err
			}
		}
		if in.CustomerID != nil {
			if _, err := s.store.Customer(tx, a.TenantID, *in.CustomerID, true); err != nil {
				return err
			}
		}
		out = PriceList{ID: id, TenantID: a.TenantID, Type: in.Type, CustomerID: in.CustomerID, Name: in.Name, Currency: in.Currency, Status: in.Status,
			ValidFrom: in.ValidFrom, ValidTo: in.ValidTo, Version: in.Version + 1, CreatedBy: creator, Revision: revision}
		var err error
		out.Note, out.KeyVersion, err = s.seal(a.TenantID, "price_list", id, "note", revision, in.Note)
		if err != nil {
			return err
		}
		if err = s.store.SavePriceList(tx, out, creating); err != nil {
			return err
		}
		if err = s.audit(tx, a, "pricelist", id, "save"); err != nil {
			return err
		}
		out, err = s.store.PriceList(tx, a.TenantID, id, false)
		return err
	})
	return out, err
}

// DeletePriceList soft-deletes the visible source while preserving its item evidence.
func (s *Service) DeletePriceList(ctx context.Context, a Actor, id int64, version int) error {
	return s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.store.DeletePriceList(tx, a.TenantID, id, version); err != nil {
			return err
		}
		return s.audit(tx, a, "pricelist", id, "delete")
	})
}

// BatchItems atomically sets or removes at most 500 exact product prices.
func (s *Service) BatchItems(ctx context.Context, a Actor, id int64, in BatchInput) (BatchResult, error) {
	in, err := normalizeBatch(in)
	if err != nil {
		return BatchResult{}, err
	}
	err = s.store.Transaction(ctx, func(tx context.Context) error {
		list, err := s.store.PriceList(tx, a.TenantID, id, true)
		if err != nil {
			return err
		}
		if list.Version != in.Version {
			return VersionConflict()
		}
		old, err := s.store.Items(tx, a.TenantID, id)
		if err != nil {
			return err
		}
		existing := map[[2]int64]Item{}
		for _, row := range old {
			existing[[2]int64{row.SPUID, row.SKUID}] = row
		}
		for _, input := range in.Items {
			if err := s.saveItem(tx, a, list, input, existing[[2]int64{input.SPUID, input.SKUID}]); err != nil {
				return err
			}
		}
		if err := s.store.BumpList(tx, a.TenantID, id, in.Version); err != nil {
			return err
		}
		return s.audit(tx, a, "pricelist", id, "items")
	})
	if err != nil {
		return BatchResult{}, err
	}
	return BatchResult{Success: len(in.Items), Failed: []BatchFailure{}, Version: in.Version + 1}, nil
}

func (s *Service) saveItem(ctx context.Context, a Actor, list PriceList, in ItemInput, old Item) error {
	if in.Delete {
		return s.store.DeleteItem(ctx, a.TenantID, list.ID, in)
	}
	product, err := s.store.Product(ctx, a.TenantID, in.SPUID, in.SKUID)
	if err != nil {
		return err
	}
	if in.SKUID > 0 && product.Currency != list.Currency {
		return Invalid()
	}
	row := Item{ID: old.ID, TenantID: a.TenantID, PriceListID: list.ID, SPUID: in.SPUID, SKUID: in.SKUID, PriceCents: in.PriceCents, MOQ: in.MOQ, Revision: old.Revision + 1}
	if row.ID == 0 {
		row.ID, err = s.store.NextID(ctx, "price_list_item")
		if err != nil {
			return err
		}
	}
	row.Note, row.KeyVersion, err = s.seal(a.TenantID, "price_list_item", row.ID, "note", row.Revision, in.Note)
	if err != nil {
		return err
	}
	return s.store.SaveItem(ctx, row, old.ID == 0)
}
