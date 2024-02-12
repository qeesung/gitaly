package repository

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const (
	httpToken = "ABCefg0999182"
)

func gitRequestValidation(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if r.Header.Get("Authorization") != httpToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	next.ServeHTTP(w, r)
}

func TestFetchRemote(t *testing.T) {
	testhelper.SkipWithWAL(t, `
Reference transaction hook is disabled in this RPC due to the reason commented in the
RPC handler body. For now, we'll wait for the RPC to be updated to do the reference updates
manually using update-ref with the fetch being just a dry-run.

Issue: https://gitlab.com/gitlab-org/gitaly/-/issues/3780`)

	t.Parallel()

	ctx := testhelper.Context(t)

	// Some of the tests require multiple calls to the clients each run struct
	// encompasses the expected data for a single run
	type run struct {
		expectedRefs     map[string]git.ObjectID
		expectedResponse *gitalypb.FetchRemoteResponse
		expectedErr      error
	}

	type setupData struct {
		repoPath string
		request  *gitalypb.FetchRemoteRequest
		runs     []run
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, cfg config.Cfg) setupData
	}{
		{
			desc: "check tags without tags",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main": commitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{},
						},
					},
				}
			},
		},
		{
			desc: "check tags with tags",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
				tagID := gittest.WriteTag(t, cfg, remoteRepoPath, "testtag", "main")

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "check tags with tags (second pull)",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
				tagID := gittest.WriteTag(t, cfg, remoteRepoPath, "testtag", "main")

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							// second time around it shouldn't have changed tags
							expectedResponse: &gitalypb.FetchRemoteResponse{},
						},
					},
				}
			},
		},
		{
			desc: "without checking for changed tags",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("main"))
				tagID := gittest.WriteTag(t, cfg, remoteRepoPath, "testtag", "main")

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						CheckTagsChanged: false,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							// TagsChanged is set to true as we have requested to not check for tags changed
							// in the request so it defaults to true.
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
						// Run a second time to ensure it is consistent
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   commitID,
								"refs/tags/testtag": tagID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "D/F conflict is resolved when pruning",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/heads/branch"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/heads/branch/conflict"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/branch": commitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
							expectedErr:      nil,
						},
					},
				}
			},
		},
		{
			desc: "D/F conflict causes failure when pruning is disabled",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/heads/branch"))
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/heads/branch/conflict"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						NoPrune: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/branch/conflict": commitID,
							},
							expectedErr: testhelper.WithInterceptedMetadataItems(
								structerr.NewInternal("preparing reference update: file directory conflict"),
								structerr.MetadataItem{Key: "conflicting_reference", Value: "refs/heads/branch"},
								structerr.MetadataItem{Key: "existing_reference", Value: "refs/heads/branch/conflict"},
							),
						},
					},
				}
			},
		},
		{
			desc: "F/D conflict is resolved when pruning",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/heads/branch"))
				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/heads/branch/conflict"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/branch/conflict": commitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
							expectedErr:      nil,
						},
					},
				}
			},
		},
		{
			desc: "F/D conflict causes failure when pruning is disabled",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/heads/branch"))
				gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/heads/branch/conflict"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						NoPrune: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/branch": commitID,
							},
							expectedErr: testhelper.WithInterceptedMetadataItems(
								structerr.NewInternal("preparing reference update: file directory conflict"),
								structerr.MetadataItem{Key: "conflicting_reference", Value: "refs/heads/branch/conflict"},
								structerr.MetadataItem{Key: "existing_reference", Value: "refs/heads/branch"},
							),
						},
					},
				}
			},
		},
		{
			desc: "with default refmaps",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				// Create the remote repository from which we're pulling from with two branches
				// that don't exist in the target repository.
				masterCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"), gittest.WithMessage("master"))
				featureCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))

				// Similar, we create the target repository with a branch that doesn't exist in the
				// source repository. This branch should get pruned by default.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("unrelated"), gittest.WithMessage("unrelated"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/feature": featureCommitID,
								"refs/heads/master":  masterCommitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "NoPrune=true with explicit Remote should not delete reference",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				masterCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"))
				unrelatedCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("unrelated"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						NoPrune: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/unrelated": unrelatedCommitID,
								"refs/heads/master":    masterCommitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "NoPrune=false with explicit Remote should delete reference",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("unrelated"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						NoPrune: false,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/master": commitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "NoPrune=false with explicit Remote should not delete reference outside of refspec",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"))
				unrelatedID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("unrelated"))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url:           remoteRepoPath,
							MirrorRefmaps: []string{"refs/heads/*:refs/remotes/my-remote/*"},
						},
						NoPrune: false,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/remotes/my-remote/master": commitID,
								"refs/heads/unrelated":          unrelatedID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "without force diverging refs not updated",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "foot", Content: "loose"}))
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "loose", Content: "foot"}))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
					},
					runs: []run{
						{
							expectedRefs:     map[string]git.ObjectID{"refs/heads/master": commitID},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
							expectedErr:      nil,
						},
					},
				}
			},
		},
		{
			desc: "with force updates diverging refs",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "foot", Content: "loose"}))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "loose", Content: "foot"}))

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: remoteRepoPath,
						},
						Force: true,
					},
					runs: []run{
						{
							expectedRefs:     map[string]git.ObjectID{"refs/heads/master": commitID},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "with explicit refmap doesn't update divergent tag",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				remoteCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/tags/v1"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "foot", Content: "loose"}))
				gittest.WriteRef(t, cfg, remoteRepoPath, "refs/heads/master", remoteCommitID)
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/tags/v1"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "loose", Content: "foot"}))
				gittest.WriteRef(t, cfg, repoPath, "refs/heads/master", commitID)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url:           remoteRepoPath,
							MirrorRefmaps: []string{"+refs/heads/master:refs/heads/master"},
						},
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/master": remoteCommitID,
								"refs/tags/v1":      commitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
							expectedErr:      nil,
						},
					},
				}
			},
		},
		{
			desc: "with explicit refmap and force updates divergent tag",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				remoteCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/tags/v1"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "foot", Content: "loose"}))
				gittest.WriteRef(t, cfg, remoteRepoPath, "refs/heads/master", remoteCommitID)
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/tags/v1"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "loose", Content: "foot"}))
				gittest.WriteRef(t, cfg, repoPath, "refs/heads/master", commitID)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url:           remoteRepoPath,
							MirrorRefmaps: []string{"refs/heads/master:refs/heads/master"},
						},
						Force: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/master": remoteCommitID,
								"refs/tags/v1":      remoteCommitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "with explicit refmap and no tags doesn't update divergent tag",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				remoteCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithReference("refs/tags/v1"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "foot", Content: "loose"}))
				gittest.WriteRef(t, cfg, remoteRepoPath, "refs/heads/master", remoteCommitID)
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/tags/v1"),
					gittest.WithTreeEntries(gittest.TreeEntry{Mode: "100644", Path: "loose", Content: "foot"}))
				gittest.WriteRef(t, cfg, repoPath, "refs/heads/master", commitID)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url:           remoteRepoPath,
							MirrorRefmaps: []string{"+refs/heads/master:refs/heads/master"},
						},
						NoTags: true,
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/master": remoteCommitID,
								"refs/tags/v1":      commitID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
						},
					},
				}
			},
		},
		{
			desc: "partial reference update",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				// We set up two branches in both repositories:
				//
				// - "main" diverges as both repositories have different commits on
				//   it.
				// - "branch" does not diverge, but is out-of-date in the local
				//   repository.
				//
				// What we want to see is that `FetchRemote()` updates the outdated
				// branch while keeping the diverging one untouched.
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithMessage("diverging-remote"), gittest.WithBranch("main"))
				remoteCommonID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithMessage("common-branch"))
				remoteUpdatedID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithParents(remoteCommonID), gittest.WithBranch("branch"))

				localRepoProto, localRepoPath := gittest.CreateRepository(t, ctx, cfg)
				localDivergingID := gittest.WriteCommit(t, cfg, localRepoPath, gittest.WithMessage("diverging-local"), gittest.WithBranch("main"))
				gittest.WriteCommit(t, cfg, localRepoPath, gittest.WithMessage("common-branch"), gittest.WithBranch("branch"))

				return setupData{
					repoPath: localRepoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: localRepoProto,
						RemoteParams: &gitalypb.Remote{
							Url:           remoteRepoPath,
							MirrorRefmaps: []string{"all_refs"},
						},
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":   localDivergingID,
								"refs/heads/branch": remoteUpdatedID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
							expectedErr:      nil,
						},
					},
				}
			},
		},
		{
			desc: "diverging reference with tags",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				// We set up a diverging branch and a tag that points to the branch
				// in the remote repository. Interestingly, even though we only
				// intend to mirror branches, we still create the tag locally even
				// though we haven't downloaded any of the objects it's pointing to.
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				remoteDivergingID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithMessage("diverging-remote"), gittest.WithBranch("main"))
				remoteTagID := gittest.WriteTag(t, cfg, remoteRepoPath, "v1.0.0", remoteDivergingID.Revision(), gittest.WriteTagConfig{
					Message: "diverging tag",
				})

				localRepoProto, localRepoPath := gittest.CreateRepository(t, ctx, cfg)
				localDivergingID := gittest.WriteCommit(t, cfg, localRepoPath, gittest.WithMessage("diverging-local"), gittest.WithBranch("main"))

				return setupData{
					repoPath: localRepoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: localRepoProto,
						RemoteParams: &gitalypb.Remote{
							Url:           remoteRepoPath,
							MirrorRefmaps: []string{"refs/heads/*:refs/heads/*"},
						},
					},
					runs: []run{
						{
							expectedRefs: map[string]git.ObjectID{
								"refs/heads/main":  localDivergingID,
								"refs/tags/v1.0.0": remoteTagID,
							},
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
							expectedErr:      nil,
						},
					},
				}
			},
		},
		{
			desc: "no repository",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				_, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						RemoteParams: &gitalypb.Remote{Url: remoteRepoPath},
					},
					runs: []run{{expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet)}},
				}
			},
		},
		{
			desc: "invalid storage",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: &gitalypb.Repository{
							StorageName:  "foobar",
							RelativePath: repoProto.RelativePath,
						},
						RemoteParams: &gitalypb.Remote{Url: remoteRepoPath},
					},
					runs: []run{{expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						"%w", storage.NewStorageNotFoundError("foobar"),
					))}},
				}
			},
		},
		{
			desc: "missing remote",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
					},
					runs: []run{{expectedErr: structerr.NewInvalidArgument("missing remote params")}},
				}
			},
		},
		{
			desc: "invalid remote URL",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository:   repoProto,
						RemoteParams: &gitalypb.Remote{Url: ""},
					},
					runs: []run{{expectedErr: structerr.NewInvalidArgument("blank or empty remote URL")}},
				}
			},
		},
		{
			desc: "/dev/null",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository:   repoProto,
						RemoteParams: &gitalypb.Remote{Url: "/dev/null"},
					},
					runs: []run{{expectedErr: structerr.NewInternal(`fetch remote: "fatal: '/dev/null' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n": exit status 128`)}},
				}
			},
		},
		{
			desc: "non existent repo via http",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gitCmdFactory := gittest.NewCommandFactory(t, cfg)
				port := gittest.HTTPServer(t, ctx, gitCmdFactory, remoteRepoPath, nil)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url:                     fmt.Sprintf("http://127.0.0.1:%d/%s", port, "invalid/repo/path.git"),
							HttpAuthorizationHeader: httpToken,
						},
					},
					runs: []run{
						{
							expectedErr: structerr.NewInternal(`fetch remote: "fatal: repository 'http://127.0.0.1:%d/invalid/repo/path.git/' not found\n": exit status 128`, port),
						},
					},
				}
			},
		},
		{
			desc: "http with token",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				masterCommitID := gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"))

				gitCmdFactory := gittest.NewCommandFactory(t, cfg)
				port := gittest.HTTPServer(t, ctx, gitCmdFactory, remoteRepoPath, gitRequestValidation)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url:                     fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(remoteRepoPath)),
							HttpAuthorizationHeader: httpToken,
						},
					},
					runs: []run{
						{
							expectedResponse: &gitalypb.FetchRemoteResponse{TagsChanged: true},
							expectedRefs:     map[string]git.ObjectID{"refs/heads/master": masterCommitID},
						},
					},
				}
			},
		},
		{
			desc: "http without token",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"))

				gitCmdFactory := gittest.NewCommandFactory(t, cfg)
				port := gittest.HTTPServer(t, ctx, gitCmdFactory, remoteRepoPath, gitRequestValidation)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(remoteRepoPath)),
						},
					},
					runs: []run{
						{
							expectedErr: structerr.NewInternal(`fetch remote: "fatal: could not read Username for 'http://127.0.0.1:%d': terminal prompts disabled\n": exit status 128`, port),
						},
					},
				}
			},
		},
		{
			desc: "http with redirect",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gittest.WriteCommit(t, cfg, remoteRepoPath, gittest.WithBranch("master"))

				gitCmdFactory := gittest.NewCommandFactory(t, cfg)
				port := gittest.HTTPServer(t, ctx, gitCmdFactory, remoteRepoPath, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
					http.Redirect(w, r, "/redirect_url", http.StatusSeeOther)
				})

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url: fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(remoteRepoPath)),
						},
					},
					runs: []run{
						{
							expectedErr: structerr.NewInternal(`fetch remote: "fatal: unable to access 'http://127.0.0.1:%d/%s/': The requested URL returned error: 303\n": exit status 128`, port, filepath.Base(remoteRepoPath)),
						},
					},
				}
			},
		},
		{
			desc: "http with timeout",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				ch := make(chan bool)

				gitCmdFactory := gittest.NewCommandFactory(t, cfg)
				port := gittest.HTTPServer(t, ctx, gitCmdFactory, remoteRepoPath, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
					<-ch
				})

				t.Cleanup(func() { close(ch) })

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.FetchRemoteRequest{
						Repository: repoProto,
						RemoteParams: &gitalypb.Remote{
							Url:                     fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(remoteRepoPath)),
							HttpAuthorizationHeader: httpToken,
						},
						Timeout: 1,
					},
					runs: []run{{expectedErr: structerr.NewInternal("fetch remote: signal: terminated: context deadline exceeded")}},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg, client := setupRepositoryService(t)
			setupData := tc.setup(t, cfg)

			for _, run := range setupData.runs {
				response, err := client.FetchRemote(ctx, setupData.request)
				testhelper.RequireGrpcError(t, run.expectedErr, err)
				testhelper.ProtoEqual(t, run.expectedResponse, response)

				var refs map[string]git.ObjectID
				refLines := text.ChompBytes(gittest.Exec(t, cfg, "-C", setupData.repoPath, "for-each-ref", `--format=%(refname) %(objectname)`))
				if refLines != "" {
					refs = make(map[string]git.ObjectID)
					for _, line := range strings.Split(refLines, "\n") {
						refname, objectID, found := strings.Cut(line, " ")
						require.True(t, found, "shouldn't have issues parsing the refs")
						refs[refname] = git.ObjectID(objectID)
					}
				}
				require.Equal(t, run.expectedRefs, refs)
			}
		})
	}
}

func TestFetchRemote_sshCommand(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	outputPath := filepath.Join(testhelper.TempDir(t), "output")

	// We ain't got a nice way to intercept the SSH call, so we just write a custom git command
	// which simply prints the GIT_SSH_COMMAND environment variable.
	gitCmdFactory := gittest.NewInterceptingCommandFactory(t, ctx, cfg, func(execEnv git.ExecutionEnvironment) string {
		//nolint:gitaly-linters
		return fmt.Sprintf(
			`#!/usr/bin/env bash

			if test -z "$GIT_SSH_COMMAND"
			then
				exec %q "$@"
			fi

			for arg in $GIT_SSH_COMMAND
			do
				case "$arg" in
				-oIdentityFile=*)
					path=$(echo "$arg" | cut -d= -f2)
					cat "$path";;
				*)
					echo "$arg";;
				esac
			done >'%s'

			exit 7
		`, execEnv.BinaryPath, outputPath)
	})

	client, addr := runRepositoryService(t, cfg, testserver.WithGitCommandFactory(gitCmdFactory))
	cfg.SocketPath = addr

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	for _, tc := range []struct {
		desc           string
		request        *gitalypb.FetchRemoteRequest
		expectedOutput string
	}{
		{
			desc: "remote parameters without SSH key",
			request: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				RemoteParams: &gitalypb.Remote{
					Url: "https://example.com",
				},
			},
			expectedOutput: "ssh\n",
		},
		{
			desc: "remote parameters with SSH key",
			request: &gitalypb.FetchRemoteRequest{
				Repository: repo,
				RemoteParams: &gitalypb.Remote{
					Url: "https://example.com",
				},
				SshKey: "mykey",
			},
			expectedOutput: "ssh\n-oIdentitiesOnly=yes\nmykey",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.FetchRemote(ctx, tc.request)
			require.Error(t, err)
			require.Contains(t, err.Error(), "fetch remote: exit status 7")

			output := testhelper.MustReadFile(t, outputPath)
			require.Equal(t, tc.expectedOutput, string(output))

			require.NoError(t, os.Remove(outputPath))
		})
	}
}

func TestFetchRemote_transaction(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	remoteCfg := testcfg.Build(t)
	_, remoteRepoPath := gittest.CreateRepository(t, ctx, remoteCfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	gittest.WriteCommit(t, remoteCfg, remoteRepoPath, gittest.WithBranch("foobar"))

	targetGitCmdFactory := gittest.NewCommandFactory(t, remoteCfg)
	port := gittest.HTTPServer(t, ctx, targetGitCmdFactory, remoteRepoPath, nil)

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	txManager := transaction.NewTrackingManager()
	client, addr := runRepositoryService(t, cfg, testserver.WithTransactionManager(txManager))
	cfg.SocketPath = addr

	repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	require.Equal(t, testhelper.GitalyOrPraefect(0, 2), len(txManager.Votes()))

	_, err = client.FetchRemote(ctx, &gitalypb.FetchRemoteRequest{
		Repository: repoProto,
		RemoteParams: &gitalypb.Remote{
			Url: fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(remoteRepoPath)),
		},
	})
	require.NoError(t, err)

	require.Equal(t, testhelper.GitalyOrPraefect(2, 4), len(txManager.Votes()))
}

func TestFetchRemote_pooledRepository(t *testing.T) {
	testhelper.SkipWithWAL(t, `
Object pools are not yet supported with transaction management.`)

	t.Parallel()

	ctx := testhelper.Context(t)

	// By default git-fetch(1) will always run with `core.alternateRefsCommand=exit 0 #`, which
	// effectively disables use of alternate refs. We can't just unset this value, so instead we
	// just write a script that knows to execute git-for-each-ref(1) as expected by this config
	// option.
	//
	// Note that we're using a separate command factory here just to ease the setup because we
	// need to recreate the other command factory with the Git configuration specified by the
	// test.
	alternateRefsCommandFactory := gittest.NewCommandFactory(t, testcfg.Build(t))
	exec := testhelper.WriteExecutable(t,
		filepath.Join(testhelper.TempDir(t), "alternate-refs"),
		[]byte(fmt.Sprintf(`#!/bin/sh
			exec %q -C "$1" for-each-ref --format='%%(objectname)'
		`, alternateRefsCommandFactory.GetExecutionEnvironment(ctx).BinaryPath)),
	)

	for _, tc := range []struct {
		desc                     string
		cfg                      config.Cfg
		shouldAnnouncePooledRefs bool
	}{
		{
			desc: "with default configuration",
		},
		{
			desc: "with alternates",
			cfg: config.Cfg{
				Git: config.Git{
					Config: []config.GitConfig{
						{
							Key:   "core.alternateRefsCommand",
							Value: exec,
						},
					},
				},
			},
			shouldAnnouncePooledRefs: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := testcfg.Build(t, testcfg.WithBase(tc.cfg))
			gitCmdFactory := gittest.NewCommandFactory(t, cfg)

			client, swocketPath := runRepositoryService(t, cfg, testserver.WithGitCommandFactory(gitCmdFactory))
			cfg.SocketPath = swocketPath

			// Create a repository that emulates an object pool. This object contains a
			// single reference with an object that is neither in the pool member nor in
			// the remote. If alternate refs are used, then Git will announce it to the
			// remote as "have".
			_, poolRepoPath := gittest.CreateRepository(t, ctx, cfg)
			poolCommitID := gittest.WriteCommit(t, cfg, poolRepoPath,
				gittest.WithBranch("pooled"),
				gittest.WithTreeEntries(gittest.TreeEntry{Path: "pool", Mode: "100644", Content: "pool contents"}),
			)

			// Create the pooled repository and link it to its pool. This is the
			// repository we're fetching into.
			pooledRepoProto, pooledRepoPath := gittest.CreateRepository(t, ctx, cfg)
			require.NoError(t, os.WriteFile(filepath.Join(pooledRepoPath, "objects", "info", "alternates"), []byte(filepath.Join(poolRepoPath, "objects")), perm.SharedFile))

			// And then finally create a third repository that emulates the remote side
			// we're fetching from. We need to create at least one reference so that Git
			// would actually try to fetch objects.
			_, remoteRepoPath := gittest.CreateRepository(t, ctx, cfg)
			gittest.WriteCommit(t, cfg, remoteRepoPath,
				gittest.WithBranch("remote"),
				gittest.WithTreeEntries(gittest.TreeEntry{Path: "remote", Mode: "100644", Content: "remote contents"}),
			)

			// Set up an HTTP server and intercept the request. This is done so that we
			// can observe the reference negotiation and check whether alternate refs
			// are announced or not.
			var requestBuffer bytes.Buffer
			port := gittest.HTTPServer(t, ctx, gitCmdFactory, remoteRepoPath, func(responseWriter http.ResponseWriter, request *http.Request, handler http.Handler) {
				closer := request.Body
				defer testhelper.MustClose(t, closer)

				request.Body = io.NopCloser(io.TeeReader(request.Body, &requestBuffer))

				handler.ServeHTTP(responseWriter, request)
			})

			// Perform the fetch.
			_, err := client.FetchRemote(ctx, &gitalypb.FetchRemoteRequest{
				Repository: pooledRepoProto,
				RemoteParams: &gitalypb.Remote{
					Url: fmt.Sprintf("http://127.0.0.1:%d/%s", port, filepath.Base(remoteRepoPath)),
				},
			})
			require.NoError(t, err)

			// This should result in the "remote" branch having been fetched into the
			// pooled repository.
			require.Equal(t,
				gittest.ResolveRevision(t, cfg, pooledRepoPath, "refs/heads/remote"),
				gittest.ResolveRevision(t, cfg, remoteRepoPath, "refs/heads/remote"),
			)

			// Verify whether alternate refs have been announced as part of the
			// reference negotiation phase.
			if tc.shouldAnnouncePooledRefs {
				require.Contains(t, requestBuffer.String(), fmt.Sprintf("have %s", poolCommitID))
			} else {
				require.NotContains(t, requestBuffer.String(), fmt.Sprintf("have %s", poolCommitID))
			}
		})
	}
}
