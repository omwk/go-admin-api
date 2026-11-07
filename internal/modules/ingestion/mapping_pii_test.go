package ingestion

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrderPublishPreflightErrorsExcludePrivateValues(t *testing.T) {
	for _, sample := range []json.RawMessage{json.RawMessage(`{"address":"PII-CANARY-123"}`), json.RawMessage(`{"address":`)} {
		backend := &MemoryPublishBackend{Samples: []json.RawMessage{sample}}
		spec := []byte(`{"schema_version":1,"resource":"order","fields":[{"target":"total_amount_cents","source":"$.address","transform":"int(value)"}]}`)
		_, result, err := (PublishGate{Backend: backend}).Publish(context.Background(), PublishRequest{VersionID: 1, Spec: spec})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "PII-CANARY-123")
		encoded, marshalErr := json.Marshal(result)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(encoded), "PII-CANARY-123")
		require.Zero(t, backend.ActiveID)
	}
}
