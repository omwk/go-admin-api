package channel

import "encoding/json"

func decodeResult(raw []byte, value any) error { return json.Unmarshal(raw, value) }
