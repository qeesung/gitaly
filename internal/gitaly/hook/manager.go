package hook

import (
	"context"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// ReferenceTransactionState is the state of the Git reference transaction. It reflects the first
// parameter of the reference-transaction hook. See githooks(1) for more information.
type ReferenceTransactionState int

const (
	// ReferenceTransactionPrepared indicates all reference updates have been queued to the
	// transaction and were locked on disk.
	ReferenceTransactionPrepared = ReferenceTransactionState(iota)
	// ReferenceTransactionCommitted indicates the reference transaction was committed and all
	// references now have their respective new value.
	ReferenceTransactionCommitted
	// ReferenceTransactionAborted indicates the transaction was aborted, no changes were
	// performed and the reference locks have been released.
	ReferenceTransactionAborted
)

// Manager is an interface providing the ability to execute Git hooks.
type Manager interface {
	// PreReceiveHook executes the pre-receive Git hook and any installed custom hooks. stdin
	// must contain all references to be updated and match the format specified in githooks(5).
	PreReceiveHook(ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error

	// PostReceiveHook executes the post-receive Git hook and any installed custom hooks. stdin
	// must contain all references to be updated and match the format specified in githooks(5).
	PostReceiveHook(ctx context.Context, repo *gitalypb.Repository, pushOptions, env []string, stdin io.Reader, stdout, stderr io.Writer) error

	// UpdateHook executes the update Git hook and any installed custom hooks for the reference
	// `ref` getting updated from `oldValue` to `newValue`.
	UpdateHook(ctx context.Context, repo *gitalypb.Repository, ref, oldValue, newValue string, env []string, stdout, stderr io.Writer) error

	// ReferenceTransactionHook executes the reference-transaction Git hook. stdin must contain
	// all references to be updated and match the format specified in githooks(5).
	ReferenceTransactionHook(ctx context.Context, state ReferenceTransactionState, env []string, stdin io.Reader) error

	// ProcReceiveRegistry provides the ProcReceiveRegistry assigned to the Manager. The registry
	// allows RPCs to hook into the proc-receive handler.
	ProcReceiveRegistry() *ProcReceiveRegistry
}

// Transaction is the interface of storagemgr.Transaction. It's used for mocking in the tests.
type Transaction interface {
	RecordInitialReferenceValues(context.Context, map[git.ReferenceName]git.ObjectID) error
	UpdateReferences(storagemgr.ReferenceUpdates)
}

// TransactionRegistry is the interface of storagemgr.TransactionRegistry. It's used for mocking
// in the tests.
type TransactionRegistry interface {
	Get(storage.TransactionID) (Transaction, error)
}

type transactionRegistry struct {
	registry *storagemgr.TransactionRegistry
}

func (r *transactionRegistry) Get(id storage.TransactionID) (Transaction, error) {
	return r.registry.Get(id)
}

// NewTransactionRegistry wraps a storagemgr.TransactionRegistry to adapt it to the interface
// used by the manager.
func NewTransactionRegistry(txRegistry *storagemgr.TransactionRegistry) TransactionRegistry {
	return &transactionRegistry{registry: txRegistry}
}

// GitLabHookManager is a hook manager containing Git hook business logic. It
// uses the GitLab API to authenticate and track ongoing hook calls.
type GitLabHookManager struct {
	cfg                 config.Cfg
	locator             storage.Locator
	logger              log.Logger
	gitCmdFactory       git.CommandFactory
	txManager           transaction.Manager
	gitlabClient        gitlab.Client
	txRegistry          TransactionRegistry
	procReceiveRegistry *ProcReceiveRegistry
	partitionManager    *storagemgr.PartitionManager
}

// ProcReceiveRegistry provides the ProcReceiveRegistry assigned to the Manager. The registry
// allows RPCs to hook into the proc-receive handler.
func (m *GitLabHookManager) ProcReceiveRegistry() *ProcReceiveRegistry {
	return m.procReceiveRegistry
}

// NewManager returns a new hook manager
func NewManager(
	cfg config.Cfg,
	locator storage.Locator,
	logger log.Logger,
	gitCmdFactory git.CommandFactory,
	txManager transaction.Manager,
	gitlabClient gitlab.Client,
	txRegistry TransactionRegistry,
	procReceiveRegistry *ProcReceiveRegistry,
	partitionManager *storagemgr.PartitionManager,
) *GitLabHookManager {
	return &GitLabHookManager{
		cfg:                 cfg,
		locator:             locator,
		logger:              logger,
		gitCmdFactory:       gitCmdFactory,
		txManager:           txManager,
		gitlabClient:        gitlabClient,
		txRegistry:          txRegistry,
		procReceiveRegistry: procReceiveRegistry,
		partitionManager:    partitionManager,
	}
}
