package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitlab-shell/client"
)

// AllowedResponse is a response for the internal gitlab api's /allowed endpoint with a subset
// of fields
type AllowedResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
}

// AllowedRequest is a request for the internal gitlab api /allowed endpoint
type AllowedRequest struct {
	Action       string `json:"action,omitempty"`
	GLRepository string `json:"gl_repository,omitempty"`
	Project      string `json:"project,omitempty"`
	Changes      string `json:"changes,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
	Env          string `json:"env,omitempty"`
	Username     string `json:"username,omitempty"`
	KeyID        string `json:"key_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
}

// marshallGitObjectDirs generates a json encoded string containing GIT_OBJECT_DIRECTORY_RELATIVE, and GIT_ALTERNATE_OBJECT_DIRECTORIES_RELATIVE
func marshallGitObjectDirs(gitObjectDirRel string, gitAltObjectDirsRel []string) (string, error) {
	envString, err := json.Marshal(map[string]interface{}{
		"GIT_OBJECT_DIRECTORY_RELATIVE":             gitObjectDirRel,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES_RELATIVE": gitAltObjectDirsRel,
	})

	if err != nil {
		return "", err
	}

	return string(envString), nil
}

// GitlabAPI is an interface for accessing the gitlab internal API
type GitlabAPI interface {
	// Allowed queries the gitlab internal api /allowed endpoint to determine if a ref change for a given repository and user is allowed
	Allowed(repo *gitalypb.Repository, glRepository, glID, glProtocol, changes string) (bool, error)
	// PreReceive queries the gitlab internal api /pre_receive to increase the reference counter
	PreReceive(glRepository string) (bool, error)
}

// gitlabAPI is a wrapper around client.GitlabNetClient with API methods for gitlab git receive hooks
type gitlabAPI struct {
	client *client.GitlabNetClient
}

// New creates a new GitlabAPI
func NewGitlabAPI(gitlabCfg config.Gitlab) (GitlabAPI, error) {
	httpClient := client.NewHTTPClient(
		gitlabCfg.URL,
		gitlabCfg.HTTPSettings.CAFile,
		gitlabCfg.HTTPSettings.CAPath,
		gitlabCfg.HTTPSettings.SelfSigned,
		uint64(gitlabCfg.HTTPSettings.ReadTimeout),
	)

	if httpClient == nil {
		return nil, fmt.Errorf("%s is not a valid url", gitlabCfg.URL)
	}

	secret, err := ioutil.ReadFile(gitlabCfg.SecretFile)
	if err != nil {
		return nil, fmt.Errorf("reading secret file: %w", err)
	}

	gitlabnetClient, err := client.NewGitlabNetClient(gitlabCfg.HTTPSettings.User, gitlabCfg.HTTPSettings.Password, string(secret), httpClient)
	if err != nil {
		return nil, fmt.Errorf("instantiating gitlab net client: %w", err)
	}

	return &gitlabAPI{
		client: gitlabnetClient,
	}, nil
}

// Allowed checks if a ref change for a given repository is allowed through the gitlab internal api /allowed endpoint
func (a *gitlabAPI) Allowed(repo *gitalypb.Repository, glRepository, glID, glProtocol, changes string) (bool, error) {
	repoPath, err := helper.GetRepoPath(repo)
	if err != nil {
		return false, fmt.Errorf("getting the repository path: %w", err)
	}

	gitObjDirVars, err := marshallGitObjectDirs(repo.GetGitObjectDirectory(), repo.GetGitAlternateObjectDirectories())
	if err != nil {
		return false, fmt.Errorf("when getting git object directories json encoded string: %w", err)
	}

	req := AllowedRequest{
		Action:       "git-receive-pack",
		GLRepository: glRepository,
		Changes:      changes,
		Protocol:     glProtocol,
		Project:      strings.Replace(repoPath, "'", "", -1),
		Env:          gitObjDirVars,
	}

	if err := req.parseAndSetGLID(glID); err != nil {
		return false, fmt.Errorf("setting gl_id: %w", err)
	}

	resp, err := a.client.Post("/allowed", &req)
	if err != nil {
		return false, fmt.Errorf("http post to gitlab api /allowed endpoint: %w", err)
	}

	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()

	var response AllowedResponse

	switch resp.StatusCode {
	case http.StatusOK,
		http.StatusMultipleChoices:

		mtype, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil {
			return false, fmt.Errorf("/allowed endpoint respond with unsupported content type: %w", err)
		}

		if mtype != "application/json" {
			return false, fmt.Errorf("/allowed endpoint respond with unsupported content type: %s", mtype)
		}

		if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return false, fmt.Errorf("decoding response from /allowed endpoint: %w", err)
		}
	default:
		return false, fmt.Errorf("gitlab api is not accessible: %d", resp.StatusCode)
	}

	return response.Status, nil
}

type preReceiveResponse struct {
	ReferenceCounterIncreased bool `json:"reference_counter_increased"`
}

// PreReceive increases the reference counter for a push for a given gl_repository through the gitlab internal API /pre_receive endpoint
func (a *gitlabAPI) PreReceive(glRepository string) (bool, error) {
	resp, err := a.client.Post("/pre_receive", map[string]string{"gl_repository": glRepository})
	if err != nil {
		return false, fmt.Errorf("http post to gitlab api /pre_receive endpoint: %w", err)
	}

	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("pre-receive call failed with status: %d", resp.StatusCode)
	}

	mtype, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return false, fmt.Errorf("/pre_receive endpoint respond with unsupported content type: %w", err)
	}

	if mtype != "application/json" {
		return false, fmt.Errorf("/pre_receive endpoint respond with unsupported content type: %s", mtype)
	}

	var result preReceiveResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decoding response from /pre_receive endpoint: %w", err)
	}

	return result.ReferenceCounterIncreased, nil
}

var glIDRegex = regexp.MustCompile(`\A[0-9]+\z`)

func (a *AllowedRequest) parseAndSetGLID(glID string) error {
	var value string

	switch {
	case strings.HasPrefix(glID, "username-"):
		a.Username = strings.TrimPrefix(glID, "username-")
		return nil
	case strings.HasPrefix(glID, "key-"):
		a.KeyID = strings.TrimPrefix(glID, "key-")
		value = a.KeyID
	case strings.HasPrefix(glID, "user-"):
		a.UserID = strings.TrimPrefix(glID, "user-")
		value = a.UserID
	}

	if !glIDRegex.MatchString(value) {
		return fmt.Errorf("gl_id='%s' is invalid", glID)
	}

	return nil
}
