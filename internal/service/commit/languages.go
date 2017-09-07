package commit

import (
	"io/ioutil"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/linguist"
	"gitlab.com/gitlab-org/gitaly/internal/service/ref"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
)

func (*server) CommitLanguages(ctx context.Context, req *pb.CommitLanguagesRequest) (*pb.CommitLanguagesResponse, error) {
	repoPath, err := helper.GetRepoPath(req.Repository)
	if err != nil {
		return nil, err
	}

	revision := string(req.Revision)
	if revision == "" {
		defaultBranch, err := ref.DefaultBranchName(ctx, repoPath)
		if err != nil {
			return nil, err
		}
		revision = string(defaultBranch)
	}

	commitID, err := lookupRevision(ctx, repoPath, revision)
	if err != nil {
		return nil, err
	}

	stats, err := linguist.Stats(ctx, repoPath, commitID)
	if err != nil {
		return nil, err
	}

	resp := &pb.CommitLanguagesResponse{}
	if len(stats) == 0 {
		return resp, nil
	}

	total := 0
	for _, count := range stats {
		total += count
	}

	if total == 0 {
		return nil, grpc.Errorf(codes.Internal, "linguist stats added up to zero: %v", stats)
	}

	for lang, count := range stats {
		l := &pb.CommitLanguagesResponse_Language{
			Name:  lang,
			Share: float32(100*count) / float32(total),
			Color: linguist.Color(lang),
		}
		resp.Languages = append(resp.Languages, l)
	}

	sort.Sort(languageSorter(resp.Languages))

	return resp, nil
}

type languageSorter []*pb.CommitLanguagesResponse_Language

func (ls languageSorter) Len() int           { return len(ls) }
func (ls languageSorter) Swap(i, j int)      { ls[i], ls[j] = ls[j], ls[i] }
func (ls languageSorter) Less(i, j int) bool { return ls[i].Share > ls[j].Share }

func lookupRevision(ctx context.Context, repoPath string, revision string) (string, error) {
	revParse, err := command.Git(ctx, "--git-dir", repoPath, "rev-parse", revision)
	if err != nil {
		return "", err
	}

	revParseBytes, err := ioutil.ReadAll(revParse)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(revParseBytes)), nil
}
