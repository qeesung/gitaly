package bundleuri

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestUploadPackGitConfig(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.BundleURI).
		Run(t, testUploadPackGitConfig)
}

func testUploadPackGitConfig(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "README", Content: "much"}),
		gittest.WithBranch("main"))

	tempDir := testhelper.TempDir(t)
	keyFile, err := os.Create(filepath.Join(tempDir, "secret.key"))
	require.NoError(t, err)
	_, err = keyFile.WriteString("super-secret-key")
	require.NoError(t, err)
	require.NoError(t, keyFile.Close())

	type setupData struct {
		sink *Sink
	}

	for _, tc := range []struct {
		desc           string
		setup          func(t *testing.T) setupData
		expectedConfig []git.ConfigPair
		expectedErr    error
	}{
		{
			desc: "no sink",
			setup: func(t *testing.T) setupData {
				return setupData{}
			},
			expectedConfig: nil,
			expectedErr:    errors.New("bundle-URI sink missing"),
		},
		{
			desc: "no bundle found",
			setup: func(t *testing.T) setupData {
				sinkDir := t.TempDir()
				sink, err := NewSink(ctx, "file://"+sinkDir+"?base_url=http://example.com&secret_key_path="+keyFile.Name())
				require.NoError(t, err)

				return setupData{
					sink: sink,
				}
			},
			expectedConfig: nil,
			expectedErr:    structerr.NewNotFound("no bundle available"),
		},
		{
			desc: "not signed",
			setup: func(t *testing.T) setupData {
				sinkDir := t.TempDir()
				sink, err := NewSink(ctx, "file://"+sinkDir)
				require.NoError(t, err)

				require.NoError(t, sink.Generate(ctx, repo))

				return setupData{
					sink: sink,
				}
			},
			expectedConfig: nil,
			expectedErr:    fmt.Errorf("signed URL: fileblob.SignedURL: bucket does not have an Options.URLSigner (code=Unimplemented)"),
		},
		{
			desc: "success",
			setup: func(t *testing.T) setupData {
				sinkDir := t.TempDir()
				sink, err := NewSink(ctx, "file://"+sinkDir+"?base_url=http://example.com&secret_key_path="+keyFile.Name())
				require.NoError(t, err)

				require.NoError(t, sink.Generate(ctx, repo))

				return setupData{
					sink: sink,
				}
			},
			expectedConfig: []git.ConfigPair{
				{
					Key:   "uploadpack.advertiseBundleURIs",
					Value: "true",
				},
				{
					Key:   "bundle.version",
					Value: "1",
				},
				{
					Key:   "bundle.mode",
					Value: "any",
				},
				{
					Key:   "bundle.default.uri",
					Value: "https://example.com/bundle.git?signed=ok",
				},
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			data := tc.setup(t)
			sink := data.sink

			actual, err := UploadPackGitConfig(ctx, sink, repoProto)

			if featureflag.BundleURI.IsEnabled(ctx) {
				require.Equal(t, tc.expectedErr, err)

				if tc.expectedConfig != nil {
					require.Equal(t, len(tc.expectedConfig), len(actual))

					for i, c := range tc.expectedConfig {
						if strings.HasSuffix(c.Key, ".uri") {
							// We cannot predict the exact signed URL Value,
							// so only check the Keys.
							require.Equal(t, c.Key, actual[i].Key)
						} else {
							require.Equal(t, c, actual[i])
						}
					}
				}
			} else {
				require.NoError(t, err)
				require.Empty(t, actual)
			}
		})
	}
}
