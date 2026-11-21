package bootstrap

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileModelIntegrationPrivateExpiry(t *testing.T) {
	// BE-IDN-025 #4: opt in to the real five-minute wait; PR CI keeps deterministic signing guards.
	if os.Getenv("FILE_MODEL_EXPIRY_ACCEPTANCE") != "1" {
		t.Skip("set FILE_MODEL_EXPIRY_ACCEPTANCE=1 for the actual five-minute expiry drill")
	}
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	w, view := f.upload(t, "private.txt", "attachment", []byte("private expiry acceptance\n"), "")
	require.Equal(t, 201, w.Code)
	link := fileModelLink(t, f, view.ID)
	require.Equal(t, 300, link.ExpiresIn)
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	require.NotEmpty(t, u.Query().Get("X-Amz-Signature"))
	require.Equal(t, "300", u.Query().Get("X-Amz-Expires"))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 6*time.Minute)
	defer cancel()
	status, _, _ := fileModelGet(ctx, t, link.URL)
	require.Equal(t, 200, status)
	bare := *u
	bare.RawQuery = ""
	status, _, _ = fileModelGet(ctx, t, bare.String())
	require.Equal(t, 403, status)
	t.Log("BE-IDN-025 #4: signed GET=200; unsigned private GET=403; expires_in=300; waiting for the original signature to expire")
	timer := time.NewTimer(302 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("expiry drill deadline exceeded")
	case <-timer.C:
	}
	status, body, _ := fileModelGet(ctx, t, link.URL)
	require.Equal(t, 403, status)
	require.Contains(t, string(body), "Request has expired")
	t.Log("BE-IDN-025 #4: original signed URL GET after 302 seconds=403; Request has expired; signature and credentials omitted")
}
