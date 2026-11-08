package share

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

func (s *Service) replayVisible(ctx context.Context, a Actor, r Route, id int64, result idem.Result) error {
	entity := "price_list"
	if strings.HasPrefix(r.Path, "/customers") {
		entity = "customer"
	}
	if id == 0 {
		var response struct {
			Data struct {
				ID int64 `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(result.Body, &response) != nil || response.Data.ID <= 0 {
			return NotFound()
		}
		id = response.Data.ID
	}
	return s.store.ReplayVisible(ctx, a.TenantID, entity, id, r.Method == "DELETE")
}
