package audit

import (
	"encoding/json"
	"errors"
)

// Purge counts use table names such as auth_token; they contain only nonnegative
// integers, never token values. Other actions retain the normal secret redaction.
func marshalAfter(event Event) ([]byte, error) {
	if event.Action != "system.tenant.purge" || event.ResourceType != "tenant" {
		return marshal(event.After)
	}
	code, codeOK := event.After["code"].(string)
	rows, rowsOK := event.After["rows"].(map[string]int64)
	if !codeOK || !rowsOK || code == "" || len(event.After) != 2 {
		return nil, errors.New("tenant purge audit requires code and integer table counts")
	}
	for _, count := range rows {
		if count < 0 {
			return nil, errors.New("tenant purge audit counts must be nonnegative")
		}
	}
	return json.Marshal(struct {
		Code string           `json:"code"`
		Rows map[string]int64 `json:"rows"`
	}{code, rows})
}
