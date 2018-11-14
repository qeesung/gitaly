package correlation

import (
	"net/http"
)

// InjectCorrelationID middleware will propagate or create a Correlation-ID for the incoming request
func InjectCorrelationID(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parent := r.Context()

		var correlationID = generateRandomCorrelationIDWithFallback(r)

		ctx := ContextWithCorrelation(parent, correlationID)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}
