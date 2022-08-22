//go:build !gitaly_test_sha256

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func testSuccessfulFindLicenseRequest(t *testing.T, cfg config.Cfg, client gitalypb.RepositoryServiceClient, rubySrv *rubyserver.Server) {
	testhelper.NewFeatureSets(featureflag.GoFindLicense).Run(t, func(t *testing.T, ctx context.Context) {
		deprecatedLicenseData := testhelper.MustReadFile(t, "testdata/gnu_license.deprecated.txt")
		licenseText := testhelper.MustReadFile(t, "testdata/gpl-2.0_license.txt")
		for _, tc := range []struct {
			desc                  string
			nonExistentRepository bool
			files                 map[string]string
			// expectedLicenseRuby is used to verify the response received from the Ruby side-car.
			// Also is it used if expectedLicenseGo is not set. Because the Licensee gem and
			// the github.com/go-enry/go-license-detector go package use different license databases
			// and different methods to detect the license, they will not always return the
			// same result. So we need to provide different expected results in some cases.
			expectedLicenseRuby *gitalypb.FindLicenseResponse
			expectedLicenseGo   *gitalypb.FindLicenseResponse
			errorContains       string
		}{
			{
				desc:                  "repository does not exist",
				nonExistentRepository: true,
				errorContains:         "GetRepoPath: not a git repository",
			},
			{
				desc: "empty if no license file in repo",
				files: map[string]string{
					"README.md": "readme content",
				},
				expectedLicenseRuby: &gitalypb.FindLicenseResponse{},
			},
			{
				desc: "high confidence mit result and less confident mit-0 result",
				files: map[string]string{
					"LICENSE": `MIT License

Copyright (c) [year] [fullname]

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.`,
				},
				expectedLicenseRuby: &gitalypb.FindLicenseResponse{
					LicenseShortName: "mit",
					LicenseUrl:       "http://choosealicense.com/licenses/mit/",
					LicenseName:      "MIT License",
					LicensePath:      "LICENSE",
				},
				expectedLicenseGo: &gitalypb.FindLicenseResponse{
					LicenseShortName: "mit",
					LicenseUrl:       "https://opensource.org/licenses/MIT",
					LicenseName:      "MIT License",
					LicensePath:      "LICENSE",
				},
			},
			{
				desc: "unknown license",
				files: map[string]string{
					"LICENSE.md": "this doesn't match any known license",
				},
				expectedLicenseRuby: &gitalypb.FindLicenseResponse{
					LicenseShortName: "other",
					LicenseUrl:       "http://choosealicense.com/licenses/other/",
					LicenseName:      "Other",
					LicensePath:      "LICENSE.md",
				},
			},
			{
				desc: "deprecated license",
				files: map[string]string{
					"LICENSE": string(deprecatedLicenseData),
				},
				expectedLicenseRuby: &gitalypb.FindLicenseResponse{
					LicenseShortName: "gpl-3.0",
					LicenseUrl:       "http://choosealicense.com/licenses/gpl-3.0/",
					LicenseName:      "GNU General Public License v3.0",
					LicensePath:      "LICENSE",
					LicenseNickname:  "GNU GPLv3",
				},
				expectedLicenseGo: &gitalypb.FindLicenseResponse{
					LicenseShortName: "gpl-3.0+",
					LicenseUrl:       "https://www.gnu.org/licenses/gpl-3.0-standalone.html",
					LicenseName:      "GNU General Public License v3.0 or later",
					LicensePath:      "LICENSE",
					// The nickname is not set because there is no nickname defined for gpl-3.0+ license.
				},
			},
			{
				desc: "license with nickname",
				files: map[string]string{
					"LICENSE": string(licenseText),
				},
				expectedLicenseRuby: &gitalypb.FindLicenseResponse{
					LicenseShortName: "gpl-2.0",
					LicenseUrl:       "http://choosealicense.com/licenses/gpl-2.0/",
					LicenseName:      "GNU General Public License v2.0",
					LicensePath:      "LICENSE",
					LicenseNickname:  "GNU GPLv2",
				},
				expectedLicenseGo: &gitalypb.FindLicenseResponse{
					LicenseShortName: "gpl-2.0",
					LicenseUrl:       "https://www.gnu.org/licenses/old-licenses/gpl-2.0-standalone.html",
					LicenseName:      "GNU General Public License v2.0 only",
					LicensePath:      "LICENSE",
					LicenseNickname:  "GNU GPLv2",
				},
			},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

				var treeEntries []gittest.TreeEntry
				for file, content := range tc.files {
					treeEntries = append(treeEntries, gittest.TreeEntry{
						Mode:    "100644",
						Path:    file,
						Content: content,
					})
				}

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(treeEntries...))

				if tc.nonExistentRepository {
					require.NoError(t, os.RemoveAll(repoPath))
				}

				resp, err := client.FindLicense(ctx, &gitalypb.FindLicenseRequest{Repository: repo})
				if tc.errorContains != "" {
					require.Error(t, err)
					require.Contains(t, err.Error(), tc.errorContains)
					return
				}

				require.NoError(t, err)
				if featureflag.GoFindLicense.IsEnabled(ctx) && tc.expectedLicenseGo != nil {
					testhelper.ProtoEqual(t, tc.expectedLicenseGo, resp)
				} else {
					testhelper.ProtoEqual(t, tc.expectedLicenseRuby, resp)
				}
			})
		}
	})
}

func testFindLicenseRequestEmptyRepo(t *testing.T, cfg config.Cfg, client gitalypb.RepositoryServiceClient, rubySrv *rubyserver.Server) {
	testhelper.NewFeatureSets(featureflag.GoFindLicense).Run(t, func(t *testing.T, ctx context.Context) {
		repo, _ := gittest.CreateRepository(ctx, t, cfg)

		resp, err := client.FindLicense(ctx, &gitalypb.FindLicenseRequest{Repository: repo})
		require.NoError(t, err)

		require.Empty(t, resp.GetLicenseShortName())
	})
}
