package operations

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/golang/protobuf/ptypes"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errNoBranches = errors.New("no branches")

// patchParser implements parsing patches from unix mailbox format used by
// 'git format-patch'.
//
// Usage follows similar pattern as the iterators in the 'db/sql' package.
// Next() should be called to see if there is a next value. If it returns true,
// the current patch can be accessed through Value(). Once Next() return false,
// the callers should call Err() to check if the iterator was exhausted or if
// an error occurred during iteration.
//
// Remember to call Close() when done with the iterator to release any resource
// taken up by it.
//
// patchParser must be used only in a request scoped context as it stores the
// context.
//
// Internally it uses Git's `mailsplit` and `mailinfo` commands to implement the
// parsing. On first call to Next(), it parses the whole mailbox and stores the
// individual patches in a temporary directory. It then iterates one by one over
// those patch files. On closing, the temporary directory is removed.
type patchParser struct {
	ctx     context.Context
	mailbox io.Reader
	gitCmd  git.CommandFactory

	// totalPatches is the total number of patches to iterate over
	totalPatches int64
	// currentIndex is the index of the currently iterated patch
	currentIndex int64
	// currentPatch holds the currently iterated upon patch
	currentPatch git2go.Patch

	// err holds any possible errors that occurred while parsing the patches.
	err error
	// tmpDir is set after the mailbox splitting has been done. tmpDir contains
	// temporary files produced by the parser. tmpDir gets cleaned up when the iterator
	// is closed.
	tmpDir string
}

func newPatchParser(ctx context.Context, gitCmd git.CommandFactory, mailbox io.Reader) *patchParser {
	return &patchParser{
		ctx:     ctx,
		mailbox: mailbox,
		gitCmd:  gitCmd,
	}
}

// Next moves the parser to the next patch and returns whether there
// is a next patch or not.
func (p *patchParser) Next() bool {
	if p.err != nil {
		return false
	}

	if p.tmpDir == "" {
		tmpDir, err := ioutil.TempDir("", "gitaly-patch-parser")
		if err != nil {
			p.err = fmt.Errorf("create tmp dir: %w", err)
			return false
		}

		p.tmpDir = tmpDir

		totalPatches, err := parseMailbox(p.ctx, p.gitCmd, tmpDir, p.mailbox)
		if err != nil {
			p.err = fmt.Errorf("parse mailbox: %w", err)
			return false
		}

		p.totalPatches = totalPatches
	}

	if p.currentIndex >= p.totalPatches {
		return false
	}

	p.currentIndex++

	patchFile, err := os.Open(filepath.Join(p.tmpDir, fmt.Sprintf("%04d", p.currentIndex)))
	if err != nil {
		p.err = fmt.Errorf("open patch file: %w", err)
		return false
	}
	defer patchFile.Close()

	patch, err := parsePatch(p.ctx, p.gitCmd, p.tmpDir, patchFile)
	if err != nil {
		p.err = fmt.Errorf("parse patch: %w", err)
		return false
	}

	p.currentPatch = patch
	return true
}

// Value returns the currently iterated upon Patch.
func (p *patchParser) Value() git2go.Patch {
	return p.currentPatch
}

// Err returns a possible error that might have occurred during
// the iteration.
func (p *patchParser) Err() error {
	return p.err
}

// Close releases the resources taken up by the parser.
func (p *patchParser) Close() error {
	if p.tmpDir != "" {
		p.tmpDir = ""
		return os.RemoveAll(p.tmpDir)
	}

	return nil
}

func (s *Server) UserApplyPatch(stream gitalypb.OperationService_UserApplyPatchServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	header := firstRequest.GetHeader()
	if header == nil {
		return status.Errorf(codes.InvalidArgument, "UserApplyPatch: empty UserApplyPatch_Header")
	}

	if err := validateUserApplyPatchHeader(header); err != nil {
		return status.Errorf(codes.InvalidArgument, "UserApplyPatch: %v", err)
	}

	requestCtx := stream.Context()

	if featureflag.IsEnabled(requestCtx, featureflag.GoUserApplyPatch) {
		return s.userApplyPatch(requestCtx, header, stream)
	}

	rubyClient, err := s.ruby.OperationServiceClient(requestCtx)
	if err != nil {
		return err
	}

	clientCtx, err := rubyserver.SetHeaders(requestCtx, s.locator, header.GetRepository())
	if err != nil {
		return err
	}

	rubyStream, err := rubyClient.UserApplyPatch(clientCtx)
	if err != nil {
		return err
	}

	if err := rubyStream.Send(firstRequest); err != nil {
		return err
	}

	err = rubyserver.Proxy(func() error {
		request, err := stream.Recv()
		if err != nil {
			return err
		}
		return rubyStream.Send(request)
	})
	if err != nil {
		return err
	}

	response, err := rubyStream.CloseAndRecv()
	if err != nil {
		return err
	}

	return stream.SendAndClose(response)
}

func (s *Server) userApplyPatch(ctx context.Context, header *gitalypb.UserApplyPatchRequest_Header, stream gitalypb.OperationService_UserApplyPatchServer) error {
	path, err := s.locator.GetRepoPath(header.Repository)
	if err != nil {
		return fmt.Errorf("get repository path: %w", err)
	}

	branchCreated := false
	targetBranch := git.NewReferenceNameFromBranchName(string(header.TargetBranch))

	repo := localrepo.New(header.Repository, s.cfg)
	parentCommitID, err := repo.ResolveRevision(ctx, targetBranch.Revision()+"^{commit}")
	if err != nil {
		if !errors.Is(err, git.ErrReferenceNotFound) {
			return fmt.Errorf("resolve target branch: %w", err)
		}

		branchCreated = true
		parentCommitID, err = discoverDefaultBranch(ctx, repo)
		if err != nil {
			if errors.Is(err, errNoBranches) {
				ctxlogrus.Extract(ctx).WithError(err).Error("failed discovering default branch")
				return errors.New("TypeError: no implicit conversion of nil into String")
			}

			return fmt.Errorf("discover default branch: %w", err)
		}
	}

	committerTime := time.Now()
	if header.Timestamp != nil {
		var err error
		committerTime, err = ptypes.Timestamp(header.Timestamp)
		if err != nil {
			return helper.ErrInvalidArgumentf("parse timestamp: %s", err)
		}
	}

	patches := newPatchParser(ctx, s.gitCmdFactory, streamio.NewReader(func() ([]byte, error) {
		req, err := stream.Recv()
		return req.GetPatches(), err
	}))
	defer func() {
		if err := patches.Close(); err != nil {
			ctxlogrus.Extract(ctx).WithError(err).Error("failed closing patch parser")
		}
	}()

	patchedCommit, err := s.git2go.Apply(ctx, git2go.ApplyParams{
		Repository:   path,
		ParentCommit: parentCommitID.String(),
		Committer:    git2go.NewSignature(string(header.User.Name), string(header.User.Email), committerTime),
		Patches:      patches,
	})
	if err != nil {
		var errApplyConflict git2go.ApplyConflictError
		if errors.As(err, &errApplyConflict) {
			return helper.ErrPreconditionFailedf(
				`Patch failed at %04d %s
When you have resolved this problem, run "git am --continue".
If you prefer to skip this patch, run "git am --skip" instead.
To restore the original branch and stop patching, run "git am --abort".
`,
				errApplyConflict.PatchNumber,
				errApplyConflict.CommitSubject,
			)
		}

		return fmt.Errorf("apply: %w", err)
	}

	oldRevision := parentCommitID
	if branchCreated {
		oldRevision = git.ZeroOID
	}

	if err := s.updateReferenceWithHooks(ctx, header.Repository, header.User, targetBranch.String(), patchedCommit, oldRevision.String()); err != nil {
		return fmt.Errorf("update reference: %w", err)
	}

	return stream.SendAndClose(&gitalypb.UserApplyPatchResponse{
		BranchUpdate: &gitalypb.OperationBranchUpdate{
			CommitId:      patchedCommit,
			BranchCreated: branchCreated,
		},
	})
}

func validateUserApplyPatchHeader(header *gitalypb.UserApplyPatchRequest_Header) error {
	if header.GetRepository() == nil {
		return fmt.Errorf("missing Repository")
	}

	if header.GetUser() == nil {
		return fmt.Errorf("missing User")
	}

	if len(header.GetTargetBranch()) == 0 {
		return fmt.Errorf("missing Branch")
	}

	return nil
}

// discoverDefaultBranch returns the commit of a repository's 'default' branch. It uses the following
// rules:
//
// 1. If there are no branches, it returns an error.
// 2. if there is only one branch, its commit is returned.
// 3. If there are multiple branches:
//    3.1 It returns the commit HEAD is pointing to, if HEAD exists.
//    3.2 It returns the commit of master branch, if it exists.
//    3.3 It lists all branches in repository and returns the first one.
func discoverDefaultBranch(ctx context.Context, repo *localrepo.Repo) (git.ObjectID, error) {
	branches, err := repo.GetBranches(ctx)
	if err != nil {
		return "", fmt.Errorf("get branches: %w", err)
	}

	if len(branches) == 0 {
		return "", errNoBranches
	}

	if len(branches) > 1 {
		for _, revision := range []git.Revision{"HEAD", "refs/heads/master"} {
			commit, err := repo.ResolveRevision(ctx, revision+"^{commit}")
			if err != nil {
				if !errors.Is(err, git.ErrReferenceNotFound) {
					return "", fmt.Errorf("resolve %q: %w", revision, err)
				}
			} else {
				return commit, nil
			}
		}
	}

	target := git.ObjectID(branches[0].Target)
	if branches[0].IsSymbolic {
		target, err = repo.ResolveRevision(ctx, target.Revision()+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("resolve default branch: %w", err)
		}
	}

	return target, nil
}

// parseMailbox takes in a stream of patches in unix mailbox format and writes them out to
// the temporary directory with sequential names "0001", "0002" and so on matching the order
// of the patches in the stream.
func parseMailbox(ctx context.Context, gitCmd git.CommandFactory, tmpDir string, mailbox io.Reader) (int64, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd, err := gitCmd.NewWithoutRepo(ctx, nil,
		git.SubCmd{
			Name:  "mailsplit",
			Flags: []git.Option{git.Flag{Name: fmt.Sprintf("-o%s", tmpDir)}},
		},
		git.WithStdout(stdout),
		git.WithStdin(mailbox),
		git.WithStderr(stderr),
	)
	if err != nil {
		return 0, fmt.Errorf("create mailsplit command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return 0, fmt.Errorf("mailsplit: %w, stderr: %q", err, stderr)
	}

	patchCount, err := strconv.ParseInt(text.ChompBytes(stdout.Bytes()), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse patch count: %w", err)
	}

	return patchCount, nil
}

// parsePatch parses a single patch. It stores intermediary state during the parsing in to
// temporary directory. It does not clean up after itself so the caller should make sure to
// remove the temporary directory.
func parsePatch(ctx context.Context, gitCmd git.CommandFactory, tmpDir string, patch io.Reader) (git2go.Patch, error) {
	commitMessageFile := filepath.Join(tmpDir, "message")
	commitDiffFile := filepath.Join(tmpDir, "diff")

	commitInfo := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd, err := gitCmd.NewWithoutRepo(ctx, nil,
		git.SubCmd{
			Name: "mailinfo",
			Args: []string{commitMessageFile, commitDiffFile},
		},
		git.WithStdin(patch),
		git.WithStdout(commitInfo),
		git.WithStderr(stderr),
	)
	if err != nil {
		return git2go.Patch{}, fmt.Errorf("create mailinfo command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return git2go.Patch{}, fmt.Errorf("mailinfo: %w, stderr: %q", err, stderr)
	}

	commitHeaders, err := textproto.NewReader(bufio.NewReader(commitInfo)).ReadMIMEHeader()
	if err != nil {
		return git2go.Patch{}, fmt.Errorf("parse commit headers: %w", err)
	}

	var authorName, authorEmail, subject, authorDate string
	for key, destinationVar := range map[string]*string{
		"Author": &authorName, "Email": &authorEmail,
		"Subject": &subject, "Date": &authorDate,
	} {
		values := commitHeaders[key]
		if len(values) == 0 {
			continue
		}

		*destinationVar = values[0]
	}

	authorTime, err := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", authorDate)
	if err != nil {
		return git2go.Patch{}, fmt.Errorf("parse author timestamp: %w", err)
	}

	commitMessage, err := ioutil.ReadFile(commitMessageFile)
	if err != nil {
		return git2go.Patch{}, fmt.Errorf("read commit message: %w", err)
	}

	commitDiff, err := ioutil.ReadFile(commitDiffFile)
	if err != nil {
		return git2go.Patch{}, fmt.Errorf("read commit diff: %w", err)
	}

	return git2go.Patch{
		Author:  git2go.NewSignature(authorName, authorEmail, authorTime),
		Subject: subject,
		Message: string(commitMessage),
		Diff:    commitDiff,
	}, nil
}
