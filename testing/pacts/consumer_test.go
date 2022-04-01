package pacts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pact-foundation/pact-go/dsl"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type Repository struct {
	RelativePath string `json:"relativePath" pact:"relativePath=@hashed/ab/cd/abcdefghij.git"`
	StorageName  string `json:"storageName" pact:"storageName=default"`
}

type CreateRepository struct {
	Repository    Repository `json:"repository"`
	DefaultBranch string     `json:"defaultBranch" pact:"defaultBranch=bWFpbg=="`
}

func (c *CreateRepository) String() string {
	j, _ := json.Marshal(c)
	return string(j)
}

func TestConsumer(t *testing.T) {
	uniqueProjectPath := uuid.New().String()

	// Create Pact connecting to local Daemon
	pact := &dsl.Pact{
		Consumer: "CreateRepository",
		Provider: "CreateRepository",
		Host:     "localhost",
	}
	defer pact.Teardown()

	// Pass in test case
	test := func() error {
		u := fmt.Sprintf("http://localhost:%d/gitaly.RepositoryService/CreateRepository", pact.Server.Port)
		req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(
			(&CreateRepository{
				Repository: Repository{
					RelativePath: uniqueProjectPath,
					StorageName:  "default",
				},
				DefaultBranch: base64.StdEncoding.EncodeToString([]byte("main")),
			}).String(),
		))

		// NOTE: by default, request bodies are expected to be sent with a Content-Type
		// of application/json. If you don't explicitly set the content-type, you
		// will get a mismatch during Verification.
		req.Header.Set("Content-Type", "application/json")

		if err != nil {
			return err
		}
		if _, err = http.DefaultClient.Do(req); err != nil {
			return err
		}

		return err
	}

	// Set up our expected interactions.
	pact.
		AddInteraction().
		Given("Repository does not exist").
		UponReceiving("A request to CreateRepository").
		WithRequest(dsl.Request{
			Method: http.MethodPost,
			Path:   dsl.String("/gitaly.RepositoryService/CreateRepository"),
			Body: map[string]interface{}{
				"defaultBranch": base64.StdEncoding.EncodeToString([]byte("main")),
				"repository": map[string]string{
					"relativePath": uniqueProjectPath,
					"storageName":  "default",
				},
			},
		}).
		WillRespondWith(dsl.Response{
			Status: 200,
			Headers: dsl.MapMatcher{
				"Content-Type": dsl.String("application/json"),
			},
			Body: map[string]interface{}{},
		})

	// Verify
	if err := pact.Verify(test); err != nil {
		log.Fatalf("Error on Verify: %v", err)
	}
}
