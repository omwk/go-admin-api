package system

import (
	"bytes"
	"encoding/json"
	"strings"
)

const auditSummaryLimit = 4096

func auditSummaries(before, after json.RawMessage) (json.RawMessage, json.RawMessage, bool, error) {
	before, err := redactAuditState(before)
	if err != nil {
		return nil, nil, false, err
	}
	after, err = redactAuditState(after)
	if err != nil {
		return nil, nil, false, err
	}
	if len(before)+len(after) <= auditSummaryLimit {
		return before, after, false, nil
	}
	beforeLimit, afterLimit := auditSummaryLimit/2, auditSummaryLimit/2
	if len(before) < beforeLimit {
		afterLimit = auditSummaryLimit - len(before)
	}
	if len(after) < afterLimit {
		beforeLimit = auditSummaryLimit - len(after)
	}
	before, err = truncateAuditState(before, beforeLimit)
	if err != nil {
		return nil, nil, false, err
	}
	after, err = truncateAuditState(after, afterLimit)
	return before, after, true, err
}

func redactAuditState(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	var state any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&state); err != nil {
		return nil, err
	}
	redactAuditValue(state)
	return json.Marshal(state)
}

func redactAuditValue(value any) {
	switch nested := value.(type) {
	case map[string]any:
		for key, value := range nested {
			if sensitiveAuditKey(key) {
				nested[key] = "***"
			} else {
				redactAuditValue(value)
			}
		}
	case []any:
		for _, item := range nested {
			redactAuditValue(item)
		}
	}
}

func sensitiveAuditKey(key string) bool {
	key = strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	for _, suffix := range []string{"passwordhash", "password", "cipher", "ciphertext", "nonce", "secret", "token"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func truncateAuditState(raw json.RawMessage, limit int) (json.RawMessage, error) {
	if len(raw) <= limit {
		return raw, nil
	}
	// The wrapper remains valid JSON; binary search includes JSON escaping in the byte budget.
	runes := []rune(string(raw))
	low, high := 0, len(runes)
	best := json.RawMessage("{}")
	for low <= high {
		mid := low + (high-low)/2
		encoded, err := json.Marshal(struct {
			Summary string `json:"summary"`
		}{Summary: string(runes[:mid])})
		if err != nil {
			return nil, err
		}
		if len(encoded) <= limit {
			best, low = encoded, mid+1
		} else {
			high = mid - 1
		}
	}
	return best, nil
}
