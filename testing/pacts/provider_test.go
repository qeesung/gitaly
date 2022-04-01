package pacts

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pact-foundation/pact-go/dsl"
	"github.com/pact-foundation/pact-go/types"
)

var (
	dir, _  = os.Getwd()
	pactDir = fmt.Sprintf("%s/pacts", dir)
)

func TestProvider(t *testing.T) {
	// Create Pact connecting to local Daemon
	pact := &dsl.Pact{
		Consumer: "MyConsumer",
		Provider: "MyProvider",
	}

	// Start provider API in the background
	go startServer()

	// Authorization middleware
	// This is your chance to modify the request before it hits your provider
	// NOTE: this should be used very carefully, as it has the potential to
	// _change_ the contract
	f := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}

	// Verify the Provider with local Pact Files
	pact.VerifyProvider(t, types.VerifyRequest{
		ProviderBaseURL:       "http://localhost:8081",
		PactURLs:              []string{filepath.ToSlash(fmt.Sprintf("%s/createrepository-createrepository.json", pactDir))},
		CustomProviderHeaders: []string{"X-API-Token: abcd"},
		RequestFilter:         f,
		StateHandlers: types.StateHandlers{
			"User foo exists": func() error {
				return nil
			},
		},
	})
}

func startServer() {
	log.Fatal(PactServerRun())
}
