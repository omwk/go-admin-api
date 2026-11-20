package storage

import (
	"context"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilePresignForcesAttachment(t *testing.T) {
	// BE-IDN-023: the response override must be included before the SDK signs it.
	for _, pathStyle := range []bool{true, false} {
		p := lookupProvider(t)
		p.PathStyle = pathStyle
		object, err := NewProviderS3(p)
		require.NoError(t, err)
		for _, key := range []string{"tenants/1/attachment/drawing.svg", "tenants/1/attachment/notes.txt"} {
			signed, err := (FileObject{S3: object}).PresignGet(context.Background(), key)
			require.NoError(t, err)
			u, err := url.Parse(signed)
			require.NoError(t, err)
			require.Equal(t, "attachment", u.Query().Get("response-content-disposition"))
			require.Equal(t, "300", u.Query().Get("X-Amz-Expires"))
			require.NotEmpty(t, u.Query().Get("X-Amz-Signature"))
		}
	}
}
