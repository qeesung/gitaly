package metadata

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/metadata"
)

const (
	// TransactionMetadataKey is the key used to store transaction
	// information in the gRPC metadata.
	TransactionMetadataKey = "transaction"
	// TransactionEnvKey is the key used to store transaction information
	// in environment variables.
	TransactionEnvKey = "REFERENCE_TRANSACTION"
)

var (
	// ErrTransactionNotFound indicates the transaction metadata could not
	// be found
	ErrTransactionNotFound = errors.New("transaction not found")
)

// Transaction stores parameters required to identify a reference
// transaction.
type Transaction struct {
	// ID is the unique identifier of a transaction
	ID uint64 `json:"id"`
	// Node is the name used to cast a vote
	Node string `json:"node"`
}

// Serialize serializes a `Transaction` into a string.
func (t Transaction) Serialize() (string, error) {
	marshalled, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(marshalled), nil
}

// FromSerialized creates a transaction from a `Serialize()`d string.
func FromSerialized(serialized string) (Transaction, error) {
	decoded, err := base64.StdEncoding.DecodeString(serialized)
	if err != nil {
		return Transaction{}, err
	}

	var tx Transaction
	if err := json.Unmarshal(decoded, &tx); err != nil {
		return Transaction{}, err
	}

	return tx, nil
}

// InjectTransaction injects reference transaction metadata into an incoming context
func InjectTransaction(ctx context.Context, tranasctionID uint64, node string) (context.Context, error) {
	transaction := Transaction{
		ID:   tranasctionID,
		Node: node,
	}

	serialized, err := transaction.Serialize()
	if err != nil {
		return nil, err
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(map[string]string{})
	} else {
		md = md.Copy()
	}
	md.Set(TransactionMetadataKey, serialized)

	return metadata.NewIncomingContext(ctx, md), nil
}

// ExtractTransaction extracts `Transaction` from an incoming context. In
// case the metadata key is not set, the function will return
// `ErrTransactionNotFound`.
func ExtractTransaction(ctx context.Context) (Transaction, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return Transaction{}, ErrTransactionNotFound
	}

	serialized := md[TransactionMetadataKey]
	if len(serialized) == 0 {
		return Transaction{}, ErrTransactionNotFound
	}

	return FromSerialized(serialized[0])
}

// Env returns a formatted environment variable mapping `TransactionEnvKey` to
// the serialized representation of `Transaction`.
func (t Transaction) Env() (string, error) {
	serialized, err := t.Serialize()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s=%s", TransactionEnvKey, serialized), nil
}

// TransactionFromEnv searches the given environment variables for
// `TransactionEnvKey` and, if present, returns its deserialized `Transaction`.
func TransactionFromEnv(envvars []string) (Transaction, error) {
	transactionKey := fmt.Sprintf("%s=", TransactionEnvKey)
	transactionEnv := ""
	for _, envvar := range envvars {
		if strings.HasPrefix(envvar, transactionKey) {
			transactionEnv = envvar[len(transactionKey):]
			break
		}
	}
	if transactionEnv == "" {
		return Transaction{}, ErrTransactionNotFound
	}

	return FromSerialized(transactionEnv)
}
