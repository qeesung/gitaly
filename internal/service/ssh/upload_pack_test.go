package ssh

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
)

func TestFailedUploadPackRequestDueToValidationError(t *testing.T) {
	server := runSSHServer(t)
	defer server.Stop()

	client := newSSHClient(t)

	tests := []struct {
		Desc string
		Req  *pb.SSHUploadPackRequest
		Code codes.Code
	}{
		{
			Desc: "Repository.RelativePath is empty",
			Req:  &pb.SSHUploadPackRequest{Repository: &pb.Repository{StorageName: "default", RelativePath: ""}},
			Code: codes.InvalidArgument,
		},
		{
			Desc: "Repository is nil",
			Req:  &pb.SSHUploadPackRequest{Repository: nil},
			Code: codes.InvalidArgument,
		},
		{
			Desc: "Data exists on first request",
			Req:  &pb.SSHUploadPackRequest{Repository: &pb.Repository{StorageName: "default", RelativePath: "path/to/repo"}, Stdin: []byte("Fail")},
			Code: codes.InvalidArgument,
		},
	}

	for _, test := range tests {
		t.Logf("test case: %s", test.Desc)
		stream, err := client.SSHUploadPack(context.Background())
		if err != nil {
			t.Fatal(err)
		}

		if err = stream.Send(test.Req); err != nil {
			t.Fatal(err)
		}
		stream.CloseSend()

		err = drainPostUploadPackResponse(stream)
		testhelper.AssertGrpcError(t, err, test.Code, "")
	}
}

func TestUploadPackCloneSuccess(t *testing.T) {
	server := runSSHServer(t)
	defer server.Stop()

	localRepoPath := path.Join(testRepoRoot, "gitlab-test-upload-pack-local")

	tests := []*exec.Cmd{
		exec.Command("git", "clone", "--depth", "1", "git@localhost:test/test.git", localRepoPath),
		exec.Command("git", "clone", "git@localhost:test/test.git", localRepoPath),
	}

	for _, cmd := range tests {
		lHead, rHead, err := testClone(t, testRepo.GetStorageName(), testRepo.GetRelativePath(), localRepoPath, cmd)
		if err != nil {
			t.Fatalf("clone failed: %v", err)
		}
		if strings.Compare(lHead, rHead) != 0 {
			t.Fatalf("local and remote head not equal. clone failed: %q != %q", lHead, rHead)
		}
	}
}

func TestUploadPackCloneFailure(t *testing.T) {
	server := runSSHServer(t)
	defer server.Stop()

	localRepoPath := path.Join(testRepoRoot, "gitlab-test-upload-pack-local")

	cmd := exec.Command("git", "clone", "git@localhost:test/test.git", localRepoPath)

	_, _, err := testClone(t, "foobar", testRepo.GetRelativePath(), localRepoPath, cmd)
	if err == nil {
		t.Fatalf("clone didn't fail")
	}
}

func testClone(t *testing.T, storageName, relativePath, localRepoPath string, cmd *exec.Cmd) (string, string, error) {
	defer os.RemoveAll(localRepoPath)
	cmd.Env = []string{
		fmt.Sprintf("GITALY_SOCKET=unix://%s", serverSocketPath),
		fmt.Sprintf("GL_STORAGENAME=%s", storageName),
		fmt.Sprintf("GL_RELATIVEPATH=%s", relativePath),
		fmt.Sprintf("GL_REPOSITORY=%s", testRepo.GetRelativePath()),
		fmt.Sprintf("GOPATH=%s", os.Getenv("GOPATH")),
		fmt.Sprintf("PATH=%s", ".:"+os.Getenv("PATH")),
		"GIT_SSH_COMMAND=gitaly-upload-pack",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("%v: %q", err, out)
	}
	if !cmd.ProcessState.Success() {
		return "", "", fmt.Errorf("Failed to run `git clone`: %q", out)
	}

	storagePath := testhelper.GitlabTestStoragePath()
	testRepoPath := path.Join(storagePath, testRepo.GetRelativePath())

	remoteHead := bytes.TrimSpace(testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "rev-parse", "master"))
	localHead := bytes.TrimSpace(testhelper.MustRunCommand(t, nil, "git", "-C", localRepoPath, "rev-parse", "master"))

	return string(localHead), string(remoteHead), nil
}

func drainPostUploadPackResponse(stream pb.SSH_SSHUploadPackClient) error {
	var err error
	for err == nil {
		_, err = stream.Recv()
	}
	return err
}
