package backup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// remoteRepository implements git repository access over GRPC
type remoteRepository struct {
	repo *gitalypb.Repository
	conn *grpc.ClientConn
}

// NewRemoteRepository returns a repository accessor that operates on a remote
// repository.
func NewRemoteRepository(repo *gitalypb.Repository, conn *grpc.ClientConn) *remoteRepository {
	return &remoteRepository{
		repo: repo,
		conn: conn,
	}
}

// ListRefs fetches the full set of refs and targets for the repository
func (rr *remoteRepository) ListRefs(ctx context.Context) ([]git.Reference, error) {
	refClient := rr.newRefClient()
	stream, err := refClient.ListRefs(ctx, &gitalypb.ListRefsRequest{
		Repository: rr.repo,
		Head:       true,
		Patterns:   [][]byte{[]byte("refs/")},
	})
	if err != nil {
		return nil, fmt.Errorf("remote repository: list refs: %w", err)
	}

	var refs []git.Reference

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("remote repository: list refs: %w", err)
		}
		for _, ref := range resp.GetReferences() {
			refs = append(refs, git.NewReference(git.ReferenceName(ref.GetName()), git.ObjectID(ref.GetTarget())))
		}
	}

	return refs, nil
}

// GetCustomHooks fetches the custom hooks archive.
func (rr *remoteRepository) GetCustomHooks(ctx context.Context, out io.Writer) error {
	repoClient := rr.newRepoClient()
	stream, err := repoClient.GetCustomHooks(ctx, &gitalypb.GetCustomHooksRequest{Repository: rr.repo})
	if err != nil {
		return fmt.Errorf("remote repository: get custom hooks: %w", err)
	}

	hooks := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		return resp.GetData(), err
	})
	if _, err := io.Copy(out, hooks); err != nil {
		return fmt.Errorf("remote repository: get custom hooks: %w", err)
	}

	return nil
}

// CreateBundle fetches a bundle that contains refs matching patterns. When
// patterns is nil all refs are bundled.
func (rr *remoteRepository) CreateBundle(ctx context.Context, out io.Writer, patterns io.Reader) error {
	if patterns != nil {
		return rr.createBundlePatterns(ctx, out, patterns)
	}

	return rr.createBundle(ctx, out)
}

func (rr *remoteRepository) createBundle(ctx context.Context, out io.Writer) error {
	repoClient := rr.newRepoClient()
	stream, err := repoClient.CreateBundle(ctx, &gitalypb.CreateBundleRequest{
		Repository: rr.repo,
	})
	if err != nil {
		return fmt.Errorf("remote repository: create bundle: %w", err)
	}

	bundle := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		if structerr.GRPCCode(err) == codes.FailedPrecondition {
			err = localrepo.ErrEmptyBundle
		}
		return resp.GetData(), err
	})

	if _, err := io.Copy(out, bundle); err != nil {
		return fmt.Errorf("remote repository: create bundle: %w", err)
	}

	return nil
}

func (rr *remoteRepository) createBundlePatterns(ctx context.Context, out io.Writer, patterns io.Reader) error {
	repoClient := rr.newRepoClient()
	stream, err := repoClient.CreateBundleFromRefList(ctx)
	if err != nil {
		return fmt.Errorf("remote repository: create bundle patterns: %w", err)
	}
	c := chunk.New(&createBundleFromRefListSender{
		stream: stream,
	})

	buf := bufio.NewReader(patterns)
	for {
		line, err := buf.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("remote repository: create bundle patterns: %w", err)
		}

		line = bytes.TrimSuffix(line, []byte("\n"))

		if err := c.Send(&gitalypb.CreateBundleFromRefListRequest{
			Repository: rr.repo,
			Patterns:   [][]byte{line},
		}); err != nil {
			return fmt.Errorf("remote repository: create bundle patterns: %w", err)
		}
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("remote repository: create bundle patterns: %w", err)
	}
	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("remote repository: create bundle patterns: %w", err)
	}

	bundle := streamio.NewReader(func() ([]byte, error) {
		resp, err := stream.Recv()
		if structerr.GRPCCode(err) == codes.FailedPrecondition {
			err = localrepo.ErrEmptyBundle
		}
		return resp.GetData(), err
	})

	if _, err := io.Copy(out, bundle); err != nil {
		return fmt.Errorf("remote repository: create bundle patterns: %w", err)
	}

	return nil
}

type createBundleFromRefListSender struct {
	stream gitalypb.RepositoryService_CreateBundleFromRefListClient
	chunk  gitalypb.CreateBundleFromRefListRequest
}

// Reset should create a fresh response message.
func (s *createBundleFromRefListSender) Reset() {
	s.chunk = gitalypb.CreateBundleFromRefListRequest{}
}

// Append should append the given item to the slice in the current response message
func (s *createBundleFromRefListSender) Append(msg proto.Message) {
	req := msg.(*gitalypb.CreateBundleFromRefListRequest)
	s.chunk.Repository = req.GetRepository()
	s.chunk.Patterns = append(s.chunk.Patterns, req.Patterns...)
}

// Send should send the current response message
func (s *createBundleFromRefListSender) Send() error {
	return s.stream.Send(&s.chunk)
}

// updateRefsSender chunks requests to the UpdateReferences RPC.
type updateRefsSender struct {
	refs []*gitalypb.UpdateReferencesRequest_Update
	send func([]*gitalypb.UpdateReferencesRequest_Update) error
}

// Reset should create a fresh response message.
func (s *updateRefsSender) Reset() {
	s.refs = s.refs[:0]
}

// Append should append the given item to the slice in the current response message
func (s *updateRefsSender) Append(msg proto.Message) {
	s.refs = append(s.refs, msg.(*gitalypb.UpdateReferencesRequest_Update))
}

// Send should send the current response message
func (s *updateRefsSender) Send() error {
	return s.send(s.refs)
}

// Remove removes the repository. Does not return an error if the repository
// cannot be found.
func (rr *remoteRepository) Remove(ctx context.Context) error {
	repoClient := rr.newRepoClient()
	_, err := repoClient.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{
		Repository: rr.repo,
	})
	switch {
	case status.Code(err) == codes.NotFound:
		return nil
	case err != nil:
		return fmt.Errorf("remote repository: remove: %w", err)
	}
	return nil
}

// ResetRefs attempts to reset the list of refs in the repository to match the
// specified refs slice. Do not include the symbolic HEAD reference in the list.
func (rr *remoteRepository) ResetRefs(ctx context.Context, refs []git.Reference) error {
	if len(refs) == 0 {
		return errors.New("empty refs list")
	}

	refClient := rr.newRefClient()

	_, err := refClient.DeleteRefs(ctx,
		&gitalypb.DeleteRefsRequest{
			Repository: rr.repo,
			// While the DeleteRefs RPC doesn't delete HEAD, we add it to the exceptions list as a
			// workaround to instruct the RPC to delete all references in the repository.
			// https://gitlab.com/gitlab-org/gitaly/-/issues/5795 tracks the enhancement to the RPC to
			// eliminate this workaround.
			ExceptWithPrefix: [][]byte{[]byte("HEAD")},
		})
	if err != nil {
		return fmt.Errorf("delete existing refs: %w", err)
	}

	stream, err := refClient.UpdateReferences(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	// We need to send the first update without the chunker, because the RPC expects the `Repository` field to be
	// empty in subsequent messages. We also need to send the first reference because the RPC expects at least one
	// update.
	if err := stream.Send(&gitalypb.UpdateReferencesRequest{
		Repository: rr.repo,
		Updates: []*gitalypb.UpdateReferencesRequest_Update{
			{
				Reference:   []byte(refs[0].Name),
				NewObjectId: []byte(refs[0].Target),
			},
		},
	}); err != nil {
		return fmt.Errorf("send initial request: %w", err)
	}

	chunker := chunk.New(&updateRefsSender{
		send: func(updates []*gitalypb.UpdateReferencesRequest_Update) error {
			return stream.Send(&gitalypb.UpdateReferencesRequest{
				Updates: updates,
			})
		},
	})

	for _, ref := range refs[1:] {
		if err := chunker.Send(&gitalypb.UpdateReferencesRequest_Update{
			Reference:   []byte(ref.Name),
			NewObjectId: []byte(ref.Target),
		}); err != nil {
			return fmt.Errorf("send ref update: %w", err)
		}
	}

	if err := chunker.Flush(); err != nil {
		return fmt.Errorf("flush ref update chunker: %w", err)
	}

	if _, err := stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("close stream: %w", err)
	}

	return nil
}

// Create creates the repository.
func (rr *remoteRepository) Create(ctx context.Context, hash git.ObjectHash, defaultBranch string) error {
	repoClient := rr.newRepoClient()
	if _, err := repoClient.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
		Repository:    rr.repo,
		ObjectFormat:  hash.ProtoFormat,
		DefaultBranch: []byte(defaultBranch),
	}); err != nil {
		return fmt.Errorf("remote repository: create: %w", err)
	}
	return nil
}

// FetchBundle fetches references from a bundle. Refs will be mirrored to the
// repository.
func (rr *remoteRepository) FetchBundle(ctx context.Context, reader io.Reader, updateHead bool) error {
	repoClient := rr.newRepoClient()
	stream, err := repoClient.FetchBundle(ctx)
	if err != nil {
		return fmt.Errorf("remote repository: fetch bundle: %w", err)
	}
	request := &gitalypb.FetchBundleRequest{Repository: rr.repo, UpdateHead: updateHead}
	bundle := streamio.NewWriter(func(p []byte) error {
		request.Data = p
		if err := stream.Send(request); err != nil {
			return err
		}

		// Only set `Repository` on the first `Send` of the stream
		request = &gitalypb.FetchBundleRequest{}

		return nil
	})
	if _, err := io.Copy(bundle, reader); err != nil {
		return fmt.Errorf("remote repository: fetch bundle: %w", err)
	}
	if _, err = stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("remote repository: fetch bundle: %w", err)
	}
	return nil
}

// SetCustomHooks updates the custom hooks for the repository.
func (rr *remoteRepository) SetCustomHooks(ctx context.Context, reader io.Reader) error {
	repoClient := rr.newRepoClient()
	stream, err := repoClient.SetCustomHooks(ctx)
	if err != nil {
		return fmt.Errorf("remote repository: set custom hooks: %w", err)
	}

	request := &gitalypb.SetCustomHooksRequest{Repository: rr.repo}
	bundle := streamio.NewWriter(func(p []byte) error {
		request.Data = p
		if err := stream.Send(request); err != nil {
			return err
		}

		// Only set `Repository` on the first `Send` of the stream
		request = &gitalypb.SetCustomHooksRequest{}

		return nil
	})
	if _, err := io.Copy(bundle, reader); err != nil {
		return fmt.Errorf("remote repository: set custom hooks: %w", err)
	}
	if _, err = stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("remote repository: set custom hooks: %w", err)
	}
	return nil
}

// ObjectHash detects the object hash used by the repository.
func (rr *remoteRepository) ObjectHash(ctx context.Context) (git.ObjectHash, error) {
	repoClient := rr.newRepoClient()

	response, err := repoClient.ObjectFormat(ctx, &gitalypb.ObjectFormatRequest{
		Repository: rr.repo,
	})
	if err != nil {
		return git.ObjectHash{}, fmt.Errorf("remote repository: object hash: %w", err)
	}

	return git.ObjectHashByProto(response.Format)
}

// HeadReference returns the current value of HEAD.
func (rr *remoteRepository) HeadReference(ctx context.Context) (git.ReferenceName, error) {
	refClient := rr.newRefClient()

	response, err := refClient.FindDefaultBranchName(ctx, &gitalypb.FindDefaultBranchNameRequest{
		Repository: rr.repo,
		HeadOnly:   true,
	})
	if err != nil {
		return "", fmt.Errorf("remote repository: head reference: %w", err)
	}

	return git.ReferenceName(response.Name), nil
}

func (rr *remoteRepository) newRepoClient() gitalypb.RepositoryServiceClient {
	return gitalypb.NewRepositoryServiceClient(rr.conn)
}

func (rr *remoteRepository) newRefClient() gitalypb.RefServiceClient {
	return gitalypb.NewRefServiceClient(rr.conn)
}

type localRepository struct {
	logger        log.Logger
	locator       storage.Locator
	gitCmdFactory git.CommandFactory
	txManager     transaction.Manager
	repoCounter   *counter.RepositoryCounter
	repo          *localrepo.Repo
}

// NewLocalRepository returns a repository accessor that operates on a local
// repository.
func NewLocalRepository(
	logger log.Logger,
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	txManager transaction.Manager,
	repoCounter *counter.RepositoryCounter,
	repo *localrepo.Repo,
) *localRepository {
	return &localRepository{
		logger:        logger,
		locator:       locator,
		gitCmdFactory: gitCmdFactory,
		txManager:     txManager,
		repoCounter:   repoCounter,
		repo:          repo,
	}
}

// ListRefs fetches the full set of refs and targets for the repository.
func (r *localRepository) ListRefs(ctx context.Context) ([]git.Reference, error) {
	refs, err := r.repo.GetReferences(ctx, "refs/")
	if err != nil {
		return nil, fmt.Errorf("local repository: list refs: %w", err)
	}

	headOID, err := r.repo.ResolveRevision(ctx, git.Revision("HEAD"))
	switch {
	case errors.Is(err, git.ErrReferenceNotFound):
		// Nothing to add
	case err != nil:
		return nil, structerr.NewInternal("local repository: list refs: %w", err)
	default:
		head := git.NewReference("HEAD", headOID)
		refs = append([]git.Reference{head}, refs...)
	}

	return refs, nil
}

// GetCustomHooks fetches the custom hooks archive.
func (r *localRepository) GetCustomHooks(ctx context.Context, out io.Writer) error {
	repoPath, err := r.locator.GetRepoPath(r.repo)
	if err != nil {
		return fmt.Errorf("get repo path: %w", err)
	}

	if err := repoutil.GetCustomHooks(ctx, r.logger, repoPath, out); err != nil {
		return fmt.Errorf("local repository: get custom hooks: %w", err)
	}
	return nil
}

// CreateBundle fetches a bundle that contains refs matching patterns. When
// patterns is nil all refs are bundled.
func (r *localRepository) CreateBundle(ctx context.Context, out io.Writer, patterns io.Reader) error {
	err := r.repo.CreateBundle(ctx, out, &localrepo.CreateBundleOpts{
		Patterns: patterns,
	})
	if err != nil {
		return fmt.Errorf("local repository: create bundle: %w", err)
	}

	return nil
}

// Remove removes the repository. Does not return an error if the repository
// cannot be found.
func (r *localRepository) Remove(ctx context.Context) error {
	err := repoutil.Remove(ctx, r.logger, r.locator, r.txManager, r.repoCounter, r.repo)
	switch {
	case status.Code(err) == codes.NotFound:
		return nil
	case err != nil:
		return fmt.Errorf("local repository: remove: %w", err)
	}
	return nil
}

// Create creates the repository.
func (r *localRepository) Create(ctx context.Context, hash git.ObjectHash, defaultBranch string) error {
	if err := repoutil.Create(
		ctx,
		r.logger,
		r.locator,
		r.gitCmdFactory,
		r.txManager,
		r.repoCounter,
		r.repo,
		func(repository *gitalypb.Repository) error { return nil },
		repoutil.WithObjectHash(hash),
		repoutil.WithBranchName(defaultBranch),
	); err != nil {
		return fmt.Errorf("local repository: create: %w", err)
	}
	return nil
}

// FetchBundle fetches references from a bundle. Refs will be mirrored to the
// repository.
func (r *localRepository) FetchBundle(ctx context.Context, reader io.Reader, updateHead bool) error {
	err := r.repo.FetchBundle(ctx, r.txManager, reader, &localrepo.FetchBundleOpts{
		UpdateHead: updateHead,
	})
	if err != nil {
		return fmt.Errorf("local repository: fetch bundle: %w", err)
	}
	return nil
}

// SetCustomHooks updates the custom hooks for the repository.
func (r *localRepository) SetCustomHooks(ctx context.Context, reader io.Reader) error {
	if err := repoutil.SetCustomHooks(
		ctx,
		r.logger,
		r.locator,
		r.txManager,
		reader,
		r.repo,
	); err != nil {
		return fmt.Errorf("local repository: set custom hooks: %w", err)
	}
	return nil
}

// ObjectHash detects the object hash used by the repository.
func (r *localRepository) ObjectHash(ctx context.Context) (git.ObjectHash, error) {
	hash, err := r.repo.ObjectHash(ctx)
	if err != nil {
		return git.ObjectHash{}, fmt.Errorf("local repository: object hash: %w", err)
	}

	return hash, nil
}

// HeadReference returns the current value of HEAD.
func (r *localRepository) HeadReference(ctx context.Context) (git.ReferenceName, error) {
	head, err := r.repo.HeadReference(ctx)
	if err != nil {
		return "", fmt.Errorf("local repository: head reference: %w", err)
	}

	return head, nil
}

// ResetRefs attempts to reset the list of refs in the repository to match the
// specified refs slice. Do not include the symbolic HEAD reference in the list.
func (r *localRepository) ResetRefs(ctx context.Context, refs []git.Reference) (returnedErr error) {
	u, err := updateref.New(ctx, r.repo)
	if err != nil {
		return fmt.Errorf("error when running creating new updater: %w", err)
	}
	defer func() {
		if err := u.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("close updater: %w", err)
		}
	}()

	if err := u.Start(); err != nil {
		return fmt.Errorf("start delete existing refs transaction: %w", err)
	}

	existingRefs, err := r.repo.GetReferences(ctx)
	if err != nil {
		return fmt.Errorf("get existing refs: %w", err)
	}
	for _, ref := range existingRefs {
		if err := u.Delete(ref.Name); err != nil {
			return fmt.Errorf("delete existing ref: %w", err)
		}
	}

	if err := u.Commit(); err != nil {
		return fmt.Errorf("commit remove existing refs: %w", err)
	}

	if err := u.Start(); err != nil {
		return fmt.Errorf("start reset refs transaction: %w", err)
	}

	for _, ref := range refs {
		if err := u.Update(ref.Name, git.ObjectID(ref.Target), ""); err != nil {
			return fmt.Errorf("reset ref: %w", err)
		}
	}

	if err := u.Commit(); err != nil {
		return fmt.Errorf("commit reset refs: %w", err)
	}

	return nil
}
