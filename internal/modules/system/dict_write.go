package system

import "context"

// Create inserts one custom dictionary and its audit event atomically.
func (s *DictService) Create(ctx context.Context, actor Actor, r DictRequest) (Dict, error) {
	if !validDict(r) {
		return Dict{}, invalidDict()
	}
	row := Dict{TenantID: actor.TenantID, Type: r.Type, Name: r.Name, Status: r.Status}
	err := s.write(ctx, actor, func(tx context.Context) error {
		if err := s.store.SaveDictionary(tx, &row, true); err != nil {
			return err
		}
		return s.audit(tx, actor, "system:dict:create", "dict", row.ID, nil, row, false)
	})
	return row, err
}

// Update preserves builtin type identities while allowing display edits.
func (s *DictService) Update(ctx context.Context, actor Actor, id int64, r DictRequest) (Dict, error) {
	if !validDict(r) {
		return Dict{}, invalidDict()
	}
	var row Dict
	err := s.write(ctx, actor, func(tx context.Context) error {
		before, err := s.store.Dictionary(tx, actor.TenantID, id, true)
		if err != nil {
			return err
		}
		if before.Builtin && before.Type != r.Type {
			return dictConflict("内置字典类型不可修改")
		}
		row = before
		row.Type, row.Name, row.Status = r.Type, r.Name, r.Status
		if err := s.store.SaveDictionary(tx, &row, false); err != nil {
			return err
		}
		return s.audit(tx, actor, "system:dict:update", "dict", id, before, row, false)
	})
	return row, err
}

// Delete physically removes only an empty, non-builtin dictionary.
func (s *DictService) Delete(ctx context.Context, actor Actor, id int64) error {
	return s.write(ctx, actor, func(tx context.Context) error {
		row, err := s.store.Dictionary(tx, actor.TenantID, id, true)
		if err != nil {
			return err
		}
		if row.Builtin {
			return dictConflict("内置字典不可删除")
		}
		items, err := s.store.DictionaryItems(tx, actor.TenantID, id, 1, 1, false)
		if err != nil {
			return err
		}
		if items.Total > 0 {
			return dictConflict("请先清空字典项")
		}
		if err := s.store.DeleteDictionary(tx, actor.TenantID, id); err != nil {
			return err
		}
		return s.audit(tx, actor, "system:dict:delete", "dict", id, row, nil, true)
	})
}

// CreateItem inserts a value under a locked, verified tenant parent.
func (s *DictService) CreateItem(ctx context.Context, actor Actor, id int64, r DictItemRequest) (DictItem, error) {
	if err := normalizeItem(&r); err != nil {
		return DictItem{}, err
	}
	row := itemFromRequest(actor.TenantID, id, r)
	err := s.write(ctx, actor, func(tx context.Context) error {
		if _, err := s.store.Dictionary(tx, actor.TenantID, id, true); err != nil {
			return err
		}
		if err := s.store.SaveDictionaryItem(tx, &row, true); err != nil {
			return err
		}
		return s.audit(tx, actor, "system:dict:create", "dict_item", row.ID, nil, row, false)
	})
	return row, err
}

// UpdateItem keeps built-in machine values stable and audits the edited projection.
func (s *DictService) UpdateItem(ctx context.Context, actor Actor, id int64, r DictItemRequest) (DictItem, error) {
	if err := normalizeItem(&r); err != nil {
		return DictItem{}, err
	}
	var row DictItem
	err := s.write(ctx, actor, func(tx context.Context) error {
		before, err := s.store.DictionaryItem(tx, actor.TenantID, id)
		if err != nil {
			return err
		}
		if _, err := s.store.Dictionary(tx, actor.TenantID, before.DictID, true); err != nil {
			return err
		}
		if before.Builtin && before.Value != r.Value {
			return dictConflict("内置字典值不可修改")
		}
		row = itemFromRequest(actor.TenantID, before.DictID, r)
		row.ID, row.Builtin = id, before.Builtin
		if err := s.store.SaveDictionaryItem(tx, &row, false); err != nil {
			return err
		}
		return s.audit(tx, actor, "system:dict:update", "dict_item", id, before, row, false)
	})
	return row, err
}

// DeleteItem protects builtin seed values and records the removed row.
func (s *DictService) DeleteItem(ctx context.Context, actor Actor, id int64) error {
	return s.write(ctx, actor, func(tx context.Context) error {
		row, err := s.store.DictionaryItem(tx, actor.TenantID, id)
		if err != nil {
			return err
		}
		if _, err := s.store.Dictionary(tx, actor.TenantID, row.DictID, true); err != nil {
			return err
		}
		if row.Builtin {
			return dictConflict("内置字典项不可删除")
		}
		if err := s.store.DeleteDictionaryItem(tx, actor.TenantID, id); err != nil {
			return err
		}
		return s.audit(tx, actor, "system:dict:delete", "dict_item", id, row, nil, true)
	})
}

func itemFromRequest(tenant, dict int64, r DictItemRequest) DictItem {
	return DictItem{TenantID: tenant, DictID: dict, Label: r.Label, Value: r.Value, Sort: r.Sort, Status: r.Status, Color: r.Color, Extra: r.Extra}
}
