package permission

import (
	"bytes"
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExportIsDeterministicAndRegistryIsUnique(t *testing.T) {
	var a, b bytes.Buffer
	require.NoError(t, Export(&a))
	require.NoError(t, Export(&b))
	require.Equal(t, a.String(), b.String())
	var definitions []Definition
	require.NoError(t, json.Unmarshal(a.Bytes(), &definitions))
	codes := []string{}
	for _, item := range definitions {
		require.NoError(t, Validate(item.Module, item.Code))
		require.NoError(t, Registered(item.Code))
		require.NotContains(t, codes, item.Code)
		codes = append(codes, item.Code)
	}
	require.True(t, sort.StringsAreSorted(codes))
	require.Error(t, Registered("system:missing:list"))
	require.Error(t, Registered("system:user"))
}
