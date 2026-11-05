package bootstrap

import (
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func exportS3Fixture(ctx context.Context, t *testing.T) (*storage.S3, string) {
	t.Helper()
	endpoint := testenv.Lookup("CH_STORAGE_ENDPOINT")
	access, secret := testenv.Lookup("CH_STORAGE_ACCESS_KEY"), testenv.Lookup("CH_STORAGE_SECRET_KEY")
	if access == "" || secret == "" {
		t.Skip("S3 integration requires CH_STORAGE_ACCESS_KEY and CH_STORAGE_SECRET_KEY")
	}
	if endpoint == "" {
		endpoint = "http://localhost:9000"
	}
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	client, err := minio.New(parsed.Host, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: parsed.Scheme == "https"})
	require.NoError(t, err)
	bucket := "export-test-" + strings.ToLower(ulid.Make().String())
	require.NoError(t, client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for object := range client.ListObjects(cleanup, bucket, minio.ListObjectsOptions{Recursive: true}) {
			require.NoError(t, object.Err)
			require.NoError(t, client.RemoveObject(cleanup, bucket, object.Key, minio.RemoveObjectOptions{}))
		}
		require.NoError(t, client.RemoveBucket(cleanup, bucket))
	})
	object, err := storage.NewS3(config.Storage{Endpoint: endpoint, Bucket: bucket, AccessKey: access, SecretKey: secret})
	require.NoError(t, err)
	return object, bucket
}

func TestExportIntegrationS3HundredThousandDownloadAndExpiry(t *testing.T) {
	// REQ-PRD-008: real private MinIO, streaming multipart, read-back validation and expiring signatures.
	f := newExportFixture(t, 100000)
	object, bucket := exportS3Fixture(f.ctx, t)
	service, err := catalog.NewExportService(NewExportStore(f.database, bucket), object, newAdminStore(f.database).Authorize)
	require.NoError(t, err)
	job, err := service.Create(f.ctx, 1, f.actor.User.ID, catalog.ExportRequest{}, nil)
	require.NoError(t, err)
	started := time.Now()
	attempt := f.begin(t, job.ID)
	materialized := time.Since(started)
	require.Less(t, materialized, 3*time.Second)
	require.NoError(t, service.Upload(f.ctx, attempt))
	elapsed := time.Since(started)
	require.Less(t, elapsed, 180*time.Second)
	persisted := f.read(t, job.ID)
	require.EqualValues(t, 100000, persisted.RowCount)
	result, err := service.Status(f.ctx, 1, f.actor.User.ID, job.ID)
	require.NoError(t, err)
	response := exportDownload(f.ctx, t, result.DownloadURL)
	require.Equal(t, 200, response.StatusCode)
	reader := csv.NewReader(response.Body)
	records := 0
	for {
		_, err := reader.Read()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		records++
	}
	require.NoError(t, response.Body.Close())
	require.Equal(t, 100001, records)
	public := strings.Split(result.DownloadURL, "?")[0]
	anonymous := exportDownload(f.ctx, t, public)
	require.Equal(t, 403, anonymous.StatusCode)
	require.NoError(t, anonymous.Body.Close())
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE export_job SET expires_at=now()+interval '31 seconds' WHERE id=?", job.ID))
	limited, err := service.Status(f.ctx, 1, f.actor.User.ID, job.ID)
	require.NoError(t, err)
	signed, err := url.Parse(limited.DownloadURL)
	require.NoError(t, err)
	seconds, err := strconv.Atoi(signed.Query().Get("X-Amz-Expires"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, seconds, 1)
	require.LessOrEqual(t, seconds, 30)
	expires, err := object.PresignGet(f.ctx, persisted.ObjectKey, time.Second)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		expired := exportDownload(f.ctx, t, expires)
		status := expired.StatusCode
		require.NoError(t, expired.Body.Close())
		return status == http.StatusForbidden
	}, 5*time.Second, 100*time.Millisecond)
	t.Logf("real S3: 100000 rows materialized in %s, uploaded, verified and published in %s; expired URL -> 403", materialized, elapsed)
}

func exportDownload(ctx context.Context, t *testing.T, location string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	require.NoError(t, err)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	require.NoError(t, err)
	return response
}
