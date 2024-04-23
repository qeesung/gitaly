package backup

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"

	_ "gocloud.dev/blob/memblob"
)

func TestResolveSink(t *testing.T) {
	ctx := testhelper.Context(t)

	isStorageServiceSink := func(bucketTy interface{}) func(t *testing.T, sink Sink) {
		return func(t *testing.T, sink Sink) {
			t.Helper()
			sssink, ok := sink.(*StorageServiceSink)
			require.True(t, ok)
			require.True(t, sssink.bucket.As(bucketTy))
		}
	}

	tmpDir := testhelper.TempDir(t)
	gsCreds := filepath.Join(tmpDir, "gs.creds")

	var (
		azureBucket    *container.Client
		gcsBucket      *storage.Client
		s3Bucket       *s3.S3
		fileblobBucket os.FileInfo
	)

	require.NoError(t, os.WriteFile(gsCreds, []byte(`
{
  "type": "service_account",
  "project_id": "hostfactory-179005",
  "private_key_id": "6253b144ccd94f50ce1224a73ffc48bda256d0a7",
  "private_key": "-----BEGIN PRIVATE KEY-----\nXXXX<KEY CONTENT OMIT HERE> \n-----END PRIVATE KEY-----\n",
  "client_email": "303721356529-compute@developer.gserviceaccount.com",
  "client_id": "116595416948414952474",
  "auth_uri": "https://accounts.google.com/o/oauth2/auth",
  "token_uri": "https://accounts.google.com/o/oauth2/token",
  "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
  "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/303724477529-compute%40developer.gserviceaccount.com"
}`), perm.SharedFile))

	for _, tc := range []struct {
		desc   string
		envs   map[string]string
		path   string
		verify func(t *testing.T, sink Sink)
		errMsg string
	}{
		{
			desc: "AWS S3",
			envs: map[string]string{
				"AWS_ACCESS_KEY_ID":     "test",
				"AWS_SECRET_ACCESS_KEY": "test",
				"AWS_REGION":            "us-east-1",
			},
			path:   "s3://bucket",
			verify: isStorageServiceSink(&s3Bucket),
		},
		{
			desc: "Google Cloud Storage",
			envs: map[string]string{
				"GOOGLE_APPLICATION_CREDENTIALS": gsCreds,
			},
			path:   "blob+gs://bucket",
			verify: isStorageServiceSink(&gcsBucket),
		},
		{
			desc: "Azure Cloud File Storage",
			envs: map[string]string{
				"AZURE_STORAGE_ACCOUNT":   "test",
				"AZURE_STORAGE_KEY":       "test",
				"AZURE_STORAGE_SAS_TOKEN": "test",
			},
			path:   "blob+bucket+azblob://bucket",
			verify: isStorageServiceSink(&azureBucket),
		},
		{
			desc:   "Fileblob",
			path:   "file://" + tmpDir,
			verify: isStorageServiceSink(&fileblobBucket),
		},
		{
			desc:   "Non-existent Fileblob",
			path:   "file://" + filepath.Join(tmpDir, "some-new-dir"),
			verify: isStorageServiceSink(&fileblobBucket),
		},
		{
			desc:   "Unspecified scheme uses fileblob",
			path:   tmpDir,
			verify: isStorageServiceSink(&fileblobBucket),
		},
		{
			desc:   "undefined",
			path:   "some:invalid:path\x00",
			errMsg: `parse "some:invalid:path\x00": net/url: invalid control character in URL`,
		},
		{
			desc:   "unrecognized scheme",
			path:   "minio://bucket",
			errMsg: `unsupported sink URI scheme: "minio"`,
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			for k, v := range tc.envs {
				t.Setenv(k, v)
			}

			sink, err := ResolveSink(ctx, tc.path)
			if tc.errMsg != "" {
				require.EqualError(t, err, tc.errMsg)
				return
			}

			require.NoError(t, err)
			defer testhelper.MustClose(t, sink)

			tc.verify(t, sink)
		})
	}
}

func TestFileBlobSink(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	tmpDir := testhelper.TempDir(t)
	tmpPath := filepath.Join(tmpDir, "another-dir")

	sink, err := ResolveSink(ctx, fmt.Sprintf("file://%s", tmpPath))
	defer testhelper.MustClose(t, sink)
	require.NoError(t, err)

	info, err := os.Stat(tmpPath)
	require.NoError(t, err)
	require.Equal(t, perm.PrivateDir, info.Mode().Perm())
}

func TestStorageServiceSink(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	sss, err := ResolveSink(ctx, "mem://test_bucket")
	require.NoError(t, err)
	defer func() { require.NoError(t, sss.Close()) }()

	t.Run("write and retrieve", func(t *testing.T) {
		const relativePath = "path/to/data"

		data := []byte("test")

		w, err := sss.GetWriter(ctx, relativePath)
		require.NoError(t, err)

		_, err = io.Copy(w, bytes.NewReader(data))
		require.NoError(t, err)

		require.NoError(t, w.Close())

		reader, err := sss.GetReader(ctx, relativePath)
		require.NoError(t, err)
		defer func() { require.NoError(t, reader.Close()) }()

		retrieved, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.Equal(t, data, retrieved)
	})

	t.Run("not existing path", func(t *testing.T) {
		reader, err := sss.GetReader(ctx, "not-existing")
		require.Equal(t, fmt.Errorf(`storage service sink: new reader for "not-existing": %w`, ErrDoesntExist), err)
		require.Nil(t, reader)
	})
}

func TestStorageServiceSink_SignedURL_notImplemented(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	tmpDir := testhelper.TempDir(t)

	for _, tc := range []struct {
		desc      string
		bucketURL string
	}{
		{
			desc:      "memory bucket",
			bucketURL: "mem://test_bucket",
		},
		{
			desc:      "fs bucket",
			bucketURL: tmpDir,
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			sss, err := ResolveSink(ctx, tc.bucketURL)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sss.Close()) })

			const relativePath = "path/to/data"

			data := []byte("test")

			w, err := sss.GetWriter(ctx, relativePath)
			require.NoError(t, err)

			_, err = io.Copy(w, bytes.NewReader(data))
			require.NoError(t, err)

			require.NoError(t, w.Close())

			_, err = sss.SignedURL(ctx, relativePath, 10*time.Minute)
			require.Error(t, err)
			require.Contains(t, err.Error(), "Unimplemented")
		})
	}
}
