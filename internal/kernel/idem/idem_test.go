package idem

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDigestRequestPreservesJSONMemberOrder(t *testing.T) {
	left := Input{Key: "client-key", Method: "POST", Route: "/api/v1/admin/shops", Body: []byte(`{"name":"a","platform_id":1}`)}
	right := left
	right.Body = []byte(`{"platform_id":1,"name":"a"}`)
	one, err := Prepare(1, 7, left)
	require.NoError(t, err)
	two, err := Prepare(1, 7, right)
	require.NoError(t, err)
	require.NotEqual(t, one.RequestHash, two.RequestHash)
	require.NotEqual(t, one.KeyDigest, DigestKey("other-key"))
}

func TestPrepareScopesKeyByUserAndUsesRouteTemplate(t *testing.T) {
	input := Input{Key: "same", Method: "post", Route: "/api/v1/admin/shops/:id/credentials", PathParams: []string{"42"}, Body: []byte("{}")}
	one, err := Prepare(1, 7, input)
	require.NoError(t, err)
	two, err := Prepare(1, 8, input)
	require.NoError(t, err)
	require.NotEqual(t, one.Scope, two.Scope)
	require.NotEqual(t, one.RequestHash, two.RequestHash)
	require.Len(t, one.KeyDigest, 64)
}

func TestResultJSONNormalizesEmptyBodyToNull(t *testing.T) {
	result, err := ResultJSON(204, nil)
	require.NoError(t, err)
	require.Equal(t, "null", string(result.Body))
}
