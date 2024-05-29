package commit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/chunk"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

const (
	defaultFlatTreeRecursion = 10
)

func validateGetTreeEntriesRequest(locator storage.Locator, in *gitalypb.GetTreeEntriesRequest) error {
	if err := locator.ValidateRepository(in.GetRepository()); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}
	if err := git.ValidateRevision(in.Revision); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	if len(in.GetPath()) == 0 {
		return structerr.NewInvalidArgument("empty path").WithDetail(&gitalypb.GetTreeEntriesError{
			Error: &gitalypb.GetTreeEntriesError_Path{
				Path: &gitalypb.PathError{
					ErrorType: gitalypb.PathError_ERROR_TYPE_EMPTY_PATH,
				},
			},
		})
	}

	return nil
}

func populateFlatPath(
	ctx context.Context,
	objectReader catfile.ObjectContentReader,
	entries []*gitalypb.TreeEntry,
) error {
	for _, entry := range entries {
		entry.FlatPath = entry.Path

		if entry.Type != gitalypb.TreeEntry_TREE {
			continue
		}

		for i := 1; i < defaultFlatTreeRecursion; i++ {
			subEntries, err := catfile.TreeEntries(ctx, objectReader, entry.CommitOid, string(entry.FlatPath))
			if err != nil {
				return err
			}

			if len(subEntries) != 1 || subEntries[0].Type != gitalypb.TreeEntry_TREE {
				break
			}

			entry.FlatPath = subEntries[0].Path
		}
	}

	return nil
}

func (s *server) sendTreeEntriesUnified(
	stream gitalypb.CommitService_GetTreeEntriesServer,
	repo *localrepo.Repo,
	revision, path string,
	recursive bool,
	skipFlatPaths bool,
	sort gitalypb.GetTreeEntriesRequest_SortBy,
	p *gitalypb.PaginationParameter,
) error {
	ctx := stream.Context()

	// While both repo.ReadTree and catfile.TreeEntries do this internally, in the case
	// of non-recursive path, we do repo.ResolveRevision, which could fail because of this.
	if path == "." {
		path = ""
	}

	readTreeOpts := []localrepo.ReadTreeOption{localrepo.WithRelativePath(path)}
	if recursive {
		readTreeOpts = append(readTreeOpts, localrepo.WithRecursive())
	}

	tree, err := repo.ReadTree(
		ctx,
		git.Revision(revision),
		readTreeOpts...,
	)
	if err != nil {
		if errors.Is(err, localrepo.ErrNotTreeish) {
			return structerr.NewInvalidArgument("path not treeish").WithDetail(&gitalypb.GetTreeEntriesError{
				Error: &gitalypb.GetTreeEntriesError_ResolveTree{
					ResolveTree: &gitalypb.ResolveRevisionError{
						Revision: []byte(revision),
					},
				},
			}).WithMetadataItems(
				structerr.MetadataItem{Key: "path", Value: path},
				structerr.MetadataItem{Key: "revision", Value: revision},
			)
		}

		if errors.Is(err, localrepo.ErrTreeNotExist) {
			return structerr.NewNotFound("revision doesn't exist").WithDetail(&gitalypb.GetTreeEntriesError{
				Error: &gitalypb.GetTreeEntriesError_ResolveTree{
					ResolveTree: &gitalypb.ResolveRevisionError{
						Revision: []byte(revision),
					},
				},
			}).WithMetadataItems(
				structerr.MetadataItem{Key: "path", Value: path},
				structerr.MetadataItem{Key: "revision", Value: revision},
			)
		}

		if errors.Is(err, git.ErrReferenceNotFound) {
			// Since we rely on repo.ResolveRevision, it could either be an invalid revision
			// or an invalid path.
			var grpcErr structerr.Error

			// Return a different gRPC error code for each case to match the old implementation.
			// We should probably change this to NewNotFound in a separate MR and FF once the
			// UseUnifiedGetTreeEntries FF is fully rolled out.
			if recursive {
				grpcErr = structerr.NewNotFound("invalid revision or path")
			} else {
				grpcErr = structerr.NewInvalidArgument("invalid revision or path")
			}

			return grpcErr.WithDetail(&gitalypb.GetTreeEntriesError{
				Error: &gitalypb.GetTreeEntriesError_ResolveTree{
					ResolveTree: &gitalypb.ResolveRevisionError{
						Revision: []byte(revision),
					},
				},
			}).WithMetadataItems(
				structerr.MetadataItem{Key: "path", Value: path},
				structerr.MetadataItem{Key: "revision", Value: revision},
			)
		}

		return fmt.Errorf("reading tree: %w", err)
	}

	var entries []*gitalypb.TreeEntry
	if err := tree.Walk(func(dir string, entry *localrepo.TreeEntry) error {
		if entry.OID == tree.OID {
			return nil
		}

		objectID, err := entry.OID.Bytes()
		if err != nil {
			return fmt.Errorf("converting tree entry OID: %w", err)
		}

		newEntry, err := git.NewTreeEntry(
			revision,
			path,
			[]byte(filepath.Join(dir, entry.Path)),
			objectID,
			[]byte(entry.Mode),
		)
		if err != nil {
			return fmt.Errorf("converting tree entry: %w", err)
		}

		entries = append(entries, newEntry)

		return nil
	}); err != nil {
		return fmt.Errorf("listing tree entries: %w", err)
	}

	// We sort before we paginate to ensure consistent results with ListLastCommitsForTree
	entries, err = sortTrees(entries, sort)
	if err != nil {
		return err
	}

	cursor := ""
	if p != nil {
		entries, cursor, err = paginateTreeEntries(ctx, entries, p)
		if err != nil {
			return err
		}
	}

	treeSender := &treeEntriesSender{stream: stream}

	if cursor != "" {
		treeSender.SetPaginationCursor(cursor)
	}

	if !recursive && !skipFlatPaths {
		// When we're not doing a recursive request, then we need to populate flat
		// paths. A flat path of a tree entry refers to the first subtree of that
		// entry which either has at least one blob or more than two subtrees. In
		// other terms, it refers to the first "non-empty" subtree such that it's
		// easy to skip navigating the intermediate subtrees which wouldn't carry
		// any interesting information anyway.
		//
		// Unfortunately, computing flat paths is _really_ inefficient: for each
		// tree entry, we recurse up to 10 levels deep into that subtree. We do so
		// by requesting the tree entries via a catfile process, which to the best
		// of my knowledge is as good as we can get. Doing this via git-ls-tree(1)
		// wouldn't fly: we'd have to spawn a separate process for each of the
		// subtrees, which is a lot of overhead.
		objectReader, cancel, err := s.catfileCache.ObjectReader(stream.Context(), repo)
		if err != nil {
			return err
		}
		defer cancel()
		if err := populateFlatPath(ctx, objectReader, entries); err != nil {
			return err
		}
	}

	sender := chunk.New(treeSender)
	for _, e := range entries {
		if err := sender.Send(e); err != nil {
			return err
		}
	}

	return sender.Flush()
}

func sortTrees(entries []*gitalypb.TreeEntry, sortBy gitalypb.GetTreeEntriesRequest_SortBy) ([]*gitalypb.TreeEntry, error) {
	if sortBy == gitalypb.GetTreeEntriesRequest_DEFAULT {
		return entries, nil
	}

	var err error

	sort.SliceStable(entries, func(i, j int) bool {
		a, firstError := toLsTreeEnum(entries[i].Type)
		b, secondError := toLsTreeEnum(entries[j].Type)

		if firstError != nil {
			err = firstError
		} else if secondError != nil {
			err = secondError
		}

		return a < b
	})

	return entries, err
}

// This is used to match the sorting order given by getLSTreeEntries
func toLsTreeEnum(input gitalypb.TreeEntry_EntryType) (localrepo.ObjectType, error) {
	switch input {
	case gitalypb.TreeEntry_TREE:
		return localrepo.Tree, nil
	case gitalypb.TreeEntry_COMMIT:
		return localrepo.Submodule, nil
	case gitalypb.TreeEntry_BLOB:
		return localrepo.Blob, nil
	default:
		return -1, localrepo.ErrParse
	}
}

type treeEntriesSender struct {
	response   *gitalypb.GetTreeEntriesResponse
	stream     gitalypb.CommitService_GetTreeEntriesServer
	cursor     string
	sentCursor bool
}

func (c *treeEntriesSender) Append(m proto.Message) {
	c.response.Entries = append(c.response.Entries, m.(*gitalypb.TreeEntry))
}

func (c *treeEntriesSender) Send() error {
	// To save bandwidth, we only send the cursor on the first response
	if !c.sentCursor {
		c.response.PaginationCursor = &gitalypb.PaginationCursor{NextCursor: c.cursor}
		c.sentCursor = true
	}

	return c.stream.Send(c.response)
}

func (c *treeEntriesSender) Reset() {
	c.response = &gitalypb.GetTreeEntriesResponse{}
}

func (c *treeEntriesSender) SetPaginationCursor(cursor string) {
	c.cursor = cursor
}

func (s *server) GetTreeEntries(in *gitalypb.GetTreeEntriesRequest, stream gitalypb.CommitService_GetTreeEntriesServer) error {
	ctx := stream.Context()

	s.logger.WithFields(log.Fields{
		"Revision": in.Revision,
		"Path":     in.Path,
	}).DebugContext(ctx, "GetTreeEntries")

	if err := validateGetTreeEntriesRequest(s.locator, in); err != nil {
		return err
	}

	repo := s.localrepo(in.GetRepository())

	revision := string(in.GetRevision())
	path := string(in.GetPath())

	return s.sendTreeEntriesUnified(stream, repo, revision, path, in.Recursive, in.SkipFlatPaths, in.GetSort(), in.GetPaginationParams())
}

func paginateTreeEntries(ctx context.Context, entries []*gitalypb.TreeEntry, p *gitalypb.PaginationParameter) ([]*gitalypb.TreeEntry, string, error) {
	limit := int(p.GetLimit())
	start, tokenType := decodePageToken(p.GetPageToken())
	index := -1

	// No token means we should start from the top
	if start == "" {
		index = 0
	} else {
		for i, entry := range entries {
			if buildEntryToken(entry, tokenType) == start {
				index = i + 1
				break
			}
		}
	}

	if index == -1 {
		return nil, "", fmt.Errorf("could not find starting OID: %s", start)
	}

	if limit == 0 {
		return nil, "", nil
	}

	if limit < 0 || (index+limit >= len(entries)) {
		return entries[index:], "", nil
	}

	paginated := entries[index : index+limit]

	newPageToken, err := encodePageToken(paginated[len(paginated)-1])
	if err != nil {
		return nil, "", fmt.Errorf("encode page token: %w", err)
	}

	return paginated, newPageToken, nil
}

func buildEntryToken(entry *gitalypb.TreeEntry, tokenType pageTokenType) string {
	if tokenType == pageTokenTypeOID {
		return entry.GetOid()
	}

	return string(entry.GetPath())
}

type pageToken struct {
	// FileName is the name of the tree entry that acts as continuation point.
	FileName string `json:"file_name"`
}

type pageTokenType bool

const (
	// pageTokenTypeOID is an old-style page token that contains the object ID a tree
	// entry is pointing to. This is ambiguous and thus deprecated.
	pageTokenTypeOID pageTokenType = false
	// pageTokenTypeFilename is a page token that contains the tree entry path.
	pageTokenTypeFilename pageTokenType = true
)

// decodePageToken decodes the given Base64-encoded page token. It returns the
// continuation point of the token and its type.
func decodePageToken(token string) (string, pageTokenType) {
	var pageToken pageToken

	decodedString, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return token, pageTokenTypeOID
	}

	if err := json.Unmarshal(decodedString, &pageToken); err != nil {
		return token, pageTokenTypeOID
	}

	return pageToken.FileName, pageTokenTypeFilename
}

// encodePageToken returns a page token with the TreeEntry's path as the continuation point for
// the next page. The page token serialized by first JSON marshaling it and then base64 encoding it.
func encodePageToken(entry *gitalypb.TreeEntry) (string, error) {
	jsonEncoded, err := json.Marshal(pageToken{FileName: string(entry.GetPath())})
	if err != nil {
		return "", err
	}

	encoded := base64.StdEncoding.EncodeToString(jsonEncoded)

	return encoded, err
}
