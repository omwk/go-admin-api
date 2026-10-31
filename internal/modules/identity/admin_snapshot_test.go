package identity

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestPhysicalDeleteSnapshotPreservesInt64(t *testing.T) {
	row := model.Role{ID: 9_007_199_254_740_993, TenantID: 1, Code: "snapshot", Name: "snapshot", DataScope: "self", Status: "active"}
	before, err := snapshot(row)
	require.NoError(t, err)
	encoded, err := json.Marshal(before)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"id":9007199254740993`)
}
