package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/go-enry/go-license-detector/v4/licensedb"
	"github.com/go-enry/go-license-detector/v4/licensedb/api"
	"github.com/go-enry/go-license-detector/v4/licensedb/filer"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/lstree"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// The `github.com/go-enry/go-license-detector` package uses https://spdx.org/licenses/
// as the source of the licenses. That package doesn't provide `nickname` info.
// But because the `nickname` is required by the FindLicense RPC interface, we had to manually
// extract the list of all license-to-nickname pairs from the Licensee license
// database which is https://github.com/github/choosealicense.com/tree/gh-pages/_licenses
// and store them here.
var nicknameByLicenseIdentifier = map[string]string{
	"agpl-3.0":           "GNU AGPLv3",
	"lgpl-3.0":           "GNU LGPLv3",
	"bsd-3-clause-clear": "Clear BSD",
	"odbl-1.0":           "ODbL",
	"ncsa":               "UIUC/NCSA",
	"lgpl-2.1":           "GNU LGPLv2.1",
	"gpl-3.0":            "GNU GPLv3",
	"gpl-2.0":            "GNU GPLv2",
}

func (s *server) FindLicense(ctx context.Context, req *gitalypb.FindLicenseRequest) (*gitalypb.FindLicenseResponse, error) {
	if featureflag.GoFindLicense.IsEnabled(ctx) {
		repo := localrepo.New(s.locator, s.gitCmdFactory, s.catfileCache, req.GetRepository())

		hasHeadRevision, err := repo.HasRevision(ctx, "HEAD")
		if err != nil {
			return nil, helper.ErrInternalf("cannot check HEAD revision: %v", err)
		}
		if !hasHeadRevision {
			return &gitalypb.FindLicenseResponse{}, nil
		}

		repoFiler := &gitFiler{ctx: ctx, repo: repo}
		detectedLicenses, err := licensedb.Detect(repoFiler)
		if err != nil {
			if errors.Is(err, licensedb.ErrNoLicenseFound) {
				if repoFiler.foundLicense {
					// In case the license is not identified, but a file containing some
					// sort of license is found, we return a predefined response.
					return &gitalypb.FindLicenseResponse{
						LicenseName:      "Other",
						LicenseShortName: "other",
						LicenseUrl:       "http://choosealicense.com/licenses/other/",
						LicensePath:      repoFiler.path,
					}, nil
				}
				return &gitalypb.FindLicenseResponse{}, nil
			}
			return nil, helper.ErrInternal(fmt.Errorf("FindLicense: Err: %w", err))
		}

		// This should not happen as the error must be returned, but let's keep it safe to avoid panics.
		if len(detectedLicenses) == 0 {
			return &gitalypb.FindLicenseResponse{}, nil
		}

		type bestMatch struct {
			shortName string
			api.Match
		}
		bestMatches := make([]bestMatch, 0, len(detectedLicenses))
		for candidate, match := range detectedLicenses {
			bestMatches = append(bestMatches, bestMatch{Match: match, shortName: candidate})
		}
		sort.Slice(bestMatches, func(i, j int) bool {
			// Because there could be multiple matches with the same confidence, we need
			// to make sure the function is consistent and returns the same license on
			// each invocation. That is why we sort by the short name as well.
			if bestMatches[i].Confidence == bestMatches[j].Confidence {
				return trimDeprecatedPrefix(bestMatches[i].shortName) < trimDeprecatedPrefix(bestMatches[j].shortName)
			}
			return bestMatches[i].Confidence > bestMatches[j].Confidence
		})

		// We also don't want to return the prefix back to the caller if it exists.
		shortName := trimDeprecatedPrefix(bestMatches[0].shortName)

		name, err := licensedb.LicenseName(shortName)
		if err != nil {
			return nil, helper.ErrInternal(fmt.Errorf("license name by id %q: %w", shortName, err))
		}

		urls, err := licensedb.LicenseURLs(shortName)
		if err != nil {
			return nil, helper.ErrInternal(fmt.Errorf("license URLs by id %q: %w", shortName, err))
		}
		var url string
		if len(urls) > 0 {
			// The URL list is returned in an ordered slice, so we just pick up the first one from the list.
			url = urls[0]
		}

		// The license identifier used by `github.com/go-enry/go-license-detector` is
		// case-sensitive, but the API requires all license identifiers to be lower-cased.
		shortName = strings.ToLower(shortName)
		nickname := nicknameByLicenseIdentifier[shortName]
		return &gitalypb.FindLicenseResponse{
			LicenseShortName: shortName,
			LicensePath:      bestMatches[0].File,
			LicenseName:      name,
			LicenseUrl:       url,
			LicenseNickname:  nickname,
		}, nil
	}

	client, err := s.ruby.RepositoryServiceClient(ctx)
	if err != nil {
		return nil, err
	}
	clientCtx, err := rubyserver.SetHeaders(ctx, s.locator, req.GetRepository())
	if err != nil {
		return nil, err
	}
	return client.FindLicense(clientCtx, req)
}

// For the deprecated licenses, the `github.com/go-enry/go-license-detector` package
// uses the "deprecated_" prefix in the identifier. But the license database stores
// information using the identifier without prefix, so we need to cut off the
// prefix before searching for full license name and license URLs.
func trimDeprecatedPrefix(name string) string {
	return strings.TrimPrefix(name, "deprecated_")
}

var readmeRegexp = regexp.MustCompile(`(readme|guidelines)(\.md|\.rst|\.html|\.txt)?$`)

type gitFiler struct {
	ctx          context.Context
	repo         *localrepo.Repo
	foundLicense bool
	path         string
}

func (f *gitFiler) ReadFile(path string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	if err := f.repo.ExecAndWait(f.ctx, git.SubCmd{
		Name: "cat-file",
		Args: []string{"blob", fmt.Sprintf("HEAD:%s", path)},
	}, git.WithStdout(&stdout), git.WithStderr(&stderr)); err != nil {
		return nil, fmt.Errorf("cat-file failed: %w, stderr: %q", err, stderr.String())
	}

	// `licensedb.Detect` only opens files that look like licenses. Failing that, it will
	// also open readme files to try to identify license files. The RPC handler needs the
	// knowledge of whether any license files were encountered, so we filter out the
	// readme files as defined in licensedb.Detect:
	// https://github.com/go-enry/go-license-detector/blob/4f2ca6af2ab943d9b5fa3a02782eebc06f79a5f4/licensedb/internal/investigation.go#L61
	//
	// This doesn't filter out the possible license files identified from the readme files which may in fact not
	// be licenses.
	if !f.foundLicense {
		f.foundLicense = !readmeRegexp.MatchString(strings.ToLower(path))
		if f.foundLicense {
			f.path = path
		}
	}

	return stdout.Bytes(), nil
}

func (f *gitFiler) ReadDir(string) ([]filer.File, error) {
	// We're doing a recursive listing returning all files at once such that we do not have to
	// call git-ls-tree(1) multiple times.
	var stderr bytes.Buffer
	cmd, err := f.repo.Exec(f.ctx, git.SubCmd{
		Name: "ls-tree",
		Flags: []git.Option{
			git.Flag{Name: "--full-tree"},
			git.Flag{Name: "-z"},
		},
		Args: []string{"HEAD"},
	}, git.WithStderr(&stderr))
	if err != nil {
		return nil, err
	}

	tree := lstree.NewParser(cmd, git.ObjectHashSHA1)

	var files []filer.File
	for {
		entry, err := tree.NextEntry()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		// Given that we're doing a recursive listing, we skip over all types which aren't
		// blobs.
		if entry.Type != lstree.Blob {
			continue
		}

		files = append(files, filer.File{
			Name:  entry.Path,
			IsDir: false,
		})
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ls-tree failed: %w, stderr: %q", err, stderr.String())
	}

	return files, nil
}

func (f *gitFiler) Close() {}

func (f *gitFiler) PathsAreAlwaysSlash() bool {
	// git ls-files uses unix slash `/`
	return true
}
