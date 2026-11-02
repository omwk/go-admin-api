package ingestion

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPayloadHashSemanticFixtures(t *testing.T) {
	// REQ-SYNC-003: formatting changes cannot create additional raw records.
	original, err := os.ReadFile("testdata/raw_original.json")
	require.NoError(t, err)
	expected, err := PayloadHash(original)
	require.NoError(t, err)
	for _, name := range []string{"raw_whitespace.json", "raw_reordered.json"} {
		payload, err := os.ReadFile("testdata/" + name)
		require.NoError(t, err)
		actual, err := PayloadHash(payload)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
}

func TestPayloadHashNumbersStringsAndArrays(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		equal       bool
	}{
		{`{"n":1}`, `{"n":1.00e0}`, true},
		{`-0`, `0.000`, true}, {`100`, `1e2`, true},
		{`0.01`, `10e-3`, true}, {`1E+10`, `10000000000`, true},
		{`9007199254740993`, `9007199254740992`, false},
		{`[1,2]`, `[2,1]`, false}, {`"a b"`, `"ab"`, false},
		{`"a"`, `"\u0061"`, true}, {`{"n":1,"n":2}`, `{"n":2}`, true},
	} {
		t.Run(tc.left+"/"+tc.right, func(t *testing.T) {
			left, err := PayloadHash(json.RawMessage(tc.left))
			require.NoError(t, err)
			right, err := PayloadHash(json.RawMessage(tc.right))
			require.NoError(t, err)
			require.Equal(t, tc.equal, left == right)
		})
	}
	for _, payload := range []string{"", "{", "{}{}", "{} null", "NaN", string([]byte{0xff})} {
		_, err := PayloadHash(json.RawMessage(payload))
		require.Error(t, err)
	}
}
