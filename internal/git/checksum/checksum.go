package checksum

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var refWhitelist = regexp.MustCompile(`HEAD|(refs/(heads|tags|keep-around|merge-requests|environments|notes)/)`)

// CalculateChecksum calculates a checksum of a repository by iterating through all refs, computing
// SHA1(ref, commit ID) for a set of allowed refs, and XOR'ing the result.
func CalculateChecksum(ctx context.Context, factory git.CommandFactory, repo *gitalypb.Repository) ([]byte, error) {
	// Get checksum here and send it along to the committed hook
	cmd, err := factory.New(ctx, repo, git.SubCmd{Name: "show-ref", Flags: []git.Option{git.Flag{Name: "--head"}}})
	if err != nil {
		return nil, err
	}

	var checksum *big.Int

	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		ref := scanner.Bytes()

		if !refWhitelist.Match(ref) {
			continue
		}

		h := sha1.New()
		// hash.Hash will never return an error.
		_, _ = h.Write(ref)

		hash := hex.EncodeToString(h.Sum(nil))
		hashIntBase16, _ := (&big.Int{}).SetString(hash, 16)

		if checksum == nil {
			checksum = hashIntBase16
		} else {
			checksum.Xor(checksum, hashIntBase16)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}

	if err := cmd.Wait(); checksum == nil || err != nil {
		if isValidRepo(ctx, factory, repo) {
			return nil, nil
		}

		return nil, status.Errorf(codes.DataLoss, "CalculateChecksum: invalid repository")
	}

	return checksum.Bytes(), nil
}

func isValidRepo(ctx context.Context, factory git.CommandFactory, repo *gitalypb.Repository) bool {
	stdout := &bytes.Buffer{}
	cmd, err := factory.New(ctx, repo,
		git.SubCmd{
			Name: "rev-parse",
			Flags: []git.Option{
				git.Flag{Name: "--is-bare-repository"},
			},
		},
		git.WithStdout(stdout),
	)
	if err != nil {
		return false
	}

	if err := cmd.Wait(); err != nil {
		return false
	}

	return strings.EqualFold(strings.TrimRight(stdout.String(), "\n"), "true")
}
