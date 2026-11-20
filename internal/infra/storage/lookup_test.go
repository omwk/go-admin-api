package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProviderS3BucketLookup(t *testing.T) {
	// BE-IDN-022: the SDK must sign the same addressing style used for object IO.
	for _, test := range []struct {
		name, input, host, path string
	}{
		{"path", `{"path_style":true}`, "s3.us-east-1.amazonaws.com", "/lookup-test/tenants/1/shop/file.csv"},
		{"auto", `{"path_style":false}`, "lookup-test.s3.dualstack.us-east-1.amazonaws.com", "/tenants/1/shop/file.csv"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := lookupProvider(t)
			require.NoError(t, json.Unmarshal([]byte(test.input), &p))
			object, err := NewProviderS3(p)
			require.NoError(t, err)
			signed, err := object.PresignGet(context.Background(), "tenants/1/shop/file.csv", time.Minute)
			require.NoError(t, err)
			u, err := url.Parse(signed)
			require.NoError(t, err)
			require.Equal(t, test.host, u.Hostname())
			require.Equal(t, test.path, u.Path)
			require.Equal(t, "60", u.Query().Get("X-Amz-Expires"))
			require.True(t, u.Query().Get("X-Amz-Signature") != "")
		})
	}
}

func lookupProvider(t *testing.T) Provider {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return Provider{ID: 1, TenantID: 1, Endpoint: "s3.us-east-1.amazonaws.com", Port: 443, UseSSL: true,
		Bucket: "lookup-test", Region: "us-east-1", AccessKey: hex.EncodeToString(key[:8]), SecretKey: hex.EncodeToString(key)}
}
