package storage

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilePublicURLHasNoSignatureAndEscapesKey(t *testing.T) {
	object, err := NewProviderS3(lookupProvider(t))
	require.NoError(t, err)
	for _, test := range []struct{ base, want string }{
		{"", "https://s3.us-east-1.amazonaws.com/lookup-test/tenants/1/attachment/%E6%96%87%E4%BB%B6%20%23%3F.png"},
		{"https://cdn.example/assets/", "https://cdn.example/assets/tenants/1/attachment/%E6%96%87%E4%BB%B6%20%23%3F.png"},
	} {
		value, err := (FileObject{S3: object, PublicBaseURL: test.base}).PublicURL("tenants/1/attachment/文件 #?.png")
		require.NoError(t, err)
		require.Equal(t, test.want, value)
		u, err := url.Parse(value)
		require.NoError(t, err)
		require.Empty(t, u.RawQuery)
	}
}
