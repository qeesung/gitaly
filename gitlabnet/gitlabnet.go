package gitlabnet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gitlab.com/gitlab-org/labkit/log"
)

const (
	internalApiPath  = "/api/v4/internal"
	secretHeaderName = "Gitlab-Shared-Secret"
	defaultUserAgent = "GitLab-Shell"
)

type ErrorResponse struct {
	Message string `json:"message"`
}

type GitlabNetClient struct {
	httpClient *HttpClient
	user       string
	password   string
	secret     string
	userAgent  string
}

func NewGitlabNetClient(
	user,
	password,
	secret string,
	httpClient *HttpClient,
) (*GitlabNetClient, error) {

	if httpClient == nil {
		return nil, fmt.Errorf("Unsupported protocol")
	}

	return &GitlabNetClient{
		httpClient: httpClient,
		user:       user,
		password:   password,
		secret:     secret,
		userAgent:  defaultUserAgent,
	}, nil
}

// SetUserAgent overrides the default user agent for the User-Agent header field
// for subsequent requests for the GitlabNetClient
func (c *GitlabNetClient) SetUserAgent(ua string) {
	c.userAgent = ua
}

func normalizePath(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if !strings.HasPrefix(path, internalApiPath) {
		path = internalApiPath + path
	}
	return path
}

func newRequest(ctx context.Context, method, host, path string, data interface{}) (*http.Request, error) {
	var jsonReader io.Reader
	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}

		jsonReader = bytes.NewReader(jsonData)
	}

	request, err := http.NewRequestWithContext(ctx, method, host+path, jsonReader)
	if err != nil {
		return nil, err
	}

	return request, nil
}

func parseError(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 399 {
		return nil
	}
	defer resp.Body.Close()
	parsedResponse := &ErrorResponse{}

	if err := json.NewDecoder(resp.Body).Decode(parsedResponse); err != nil {
		return fmt.Errorf("Internal API error (%v)", resp.StatusCode)
	} else {
		return fmt.Errorf(parsedResponse.Message)
	}

}

func (c *GitlabNetClient) Get(ctx context.Context, path string) (*http.Response, error) {
	return c.DoRequest(ctx, http.MethodGet, normalizePath(path), nil)
}

func (c *GitlabNetClient) Post(ctx context.Context, path string, data interface{}) (*http.Response, error) {
	return c.DoRequest(ctx, http.MethodPost, normalizePath(path), data)
}

func (c *GitlabNetClient) DoRequest(ctx context.Context, method, path string, data interface{}) (*http.Response, error) {
	request, err := newRequest(ctx, method, c.httpClient.Host, path, data)
	if err != nil {
		return nil, err
	}

	user, password := c.user, c.password
	if user != "" && password != "" {
		request.SetBasicAuth(user, password)
	}

	encodedSecret := base64.StdEncoding.EncodeToString([]byte(c.secret))
	request.Header.Set(secretHeaderName, encodedSecret)

	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("User-Agent", c.userAgent)
	request.Close = true

	start := time.Now()
	response, err := c.httpClient.Do(request)
	fields := log.Fields{
		"method":      method,
		"url":         request.URL.String(),
		"duration_ms": time.Since(start) / time.Millisecond,
	}
	logger := log.WithContextFields(ctx, fields)

	if err != nil {
		logger.WithError(err).Error("Internal API unreachable")
		return nil, fmt.Errorf("Internal API unreachable")
	}

	if response != nil {
		logger = logger.WithField("status", response.StatusCode)
	}
	if err := parseError(response); err != nil {
		logger.WithError(err).Error("Internal API error")
		return nil, err
	}

	if response.ContentLength >= 0 {
		logger = logger.WithField("content_length_bytes", response.ContentLength)
	}

	logger.Info("Finished HTTP request")

	return response, nil
}
