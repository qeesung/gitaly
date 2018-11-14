package correlation

import (
	"context"
)

type ctxKey int

const keyCorrelationID ctxKey = iota

// ExtractFromContext extracts the CollectionID from the provided context
// Returns an empty string if it's unable to extract the CorrelationID for
// any reason.
func ExtractFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	id := ctx.Value(keyCorrelationID)

	str, ok := id.(string)
	if !ok {
		return ""
	}

	return str
}

// ContextWithCorrelation will create a new context containing the Correlation-ID value
func ContextWithCorrelation(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, keyCorrelationID, correlationID)
}
