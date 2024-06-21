package service

import (
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/limithandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/limiter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/streamcache"
)

// Dependencies assembles set of components required by different kinds of services.
type Dependencies struct {
	Logger              log.Logger
	Cfg                 config.Cfg
	GitalyHookManager   gitalyhook.Manager
	TransactionManager  transaction.Manager
	StorageLocator      storage.Locator
	ClientPool          *client.Pool
	GitCmdFactory       git.CommandFactory
	BackchannelRegistry *backchannel.Registry
	GitlabClient        gitlab.Client
	CatfileCache        catfile.Cache
	DiskCache           cache.Cache
	PackObjectsCache    streamcache.Cache
	PackObjectsLimiter  limiter.Limiter
	LimitHandler        *limithandler.LimiterMiddleware
	RepositoryCounter   *counter.RepositoryCounter
	UpdaterWithHooks    *updateref.UpdaterWithHooks
	HousekeepingManager housekeepingmgr.Manager
	TransactionRegistry *storagemgr.TransactionRegistry
	PartitionManager    *storagemgr.PartitionManager
	BackupSink          backup.Sink
	BackupLocator       backup.Locator
	BundleURISink       *bundleuri.Sink
	ProcReceiveRegistry *gitalyhook.ProcReceiveRegistry
	InProgressTracker   *InProgressTracker
}

// GetLogger returns the logger.
func (dc *Dependencies) GetLogger() log.Logger {
	return dc.Logger
}

// GetCfg returns service configuration.
func (dc *Dependencies) GetCfg() config.Cfg {
	return dc.Cfg
}

// GetHookManager returns hook manager.
func (dc *Dependencies) GetHookManager() gitalyhook.Manager {
	return dc.GitalyHookManager
}

// GetTxManager returns transaction manager.
func (dc *Dependencies) GetTxManager() transaction.Manager {
	return dc.TransactionManager
}

// GetLocator returns storage locator.
func (dc *Dependencies) GetLocator() storage.Locator {
	return dc.StorageLocator
}

// GetConnsPool returns gRPC connection pool.
func (dc *Dependencies) GetConnsPool() *client.Pool {
	return dc.ClientPool
}

// GetGitCmdFactory returns git commands factory.
func (dc *Dependencies) GetGitCmdFactory() git.CommandFactory {
	return dc.GitCmdFactory
}

// GetBackchannelRegistry returns a registry of the backchannels.
func (dc *Dependencies) GetBackchannelRegistry() *backchannel.Registry {
	return dc.BackchannelRegistry
}

// GetGitlabClient returns client to access GitLab API.
func (dc *Dependencies) GetGitlabClient() gitlab.Client {
	return dc.GitlabClient
}

// GetCatfileCache returns catfile cache.
func (dc *Dependencies) GetCatfileCache() catfile.Cache {
	return dc.CatfileCache
}

// GetDiskCache returns the disk cache.
func (dc *Dependencies) GetDiskCache() cache.Cache {
	return dc.DiskCache
}

// GetPackObjectsCache returns the pack-objects cache.
func (dc *Dependencies) GetPackObjectsCache() streamcache.Cache {
	return dc.PackObjectsCache
}

// GetLimitHandler returns the RPC limit handler.
func (dc *Dependencies) GetLimitHandler() *limithandler.LimiterMiddleware {
	return dc.LimitHandler
}

// GetRepositoryCounter returns the repository counter.
func (dc *Dependencies) GetRepositoryCounter() *counter.RepositoryCounter {
	return dc.RepositoryCounter
}

// GetUpdaterWithHooks returns the updater with hooks executor.
func (dc *Dependencies) GetUpdaterWithHooks() *updateref.UpdaterWithHooks {
	return dc.UpdaterWithHooks
}

// GetHousekeepingManager returns the housekeeping manager.
func (dc *Dependencies) GetHousekeepingManager() housekeepingmgr.Manager {
	return dc.HousekeepingManager
}

// GetPackObjectsLimiter returns the pack-objects limiter.
func (dc *Dependencies) GetPackObjectsLimiter() limiter.Limiter {
	return dc.PackObjectsLimiter
}

// GetTransactionRegistry returns the TransactionRegistry.
func (dc *Dependencies) GetTransactionRegistry() *storagemgr.TransactionRegistry {
	return dc.TransactionRegistry
}

// GetPartitionManager returns the PartitionManager.
func (dc *Dependencies) GetPartitionManager() *storagemgr.PartitionManager {
	return dc.PartitionManager
}

// GetBackupSink returns the backup.Sink.
func (dc *Dependencies) GetBackupSink() backup.Sink {
	return dc.BackupSink
}

// GetBackupLocator returns the backup.Locator.
func (dc *Dependencies) GetBackupLocator() backup.Locator {
	return dc.BackupLocator
}

// GetBundleURISink return the bundleuri.Sink.
func (dc *Dependencies) GetBundleURISink() *bundleuri.Sink {
	return dc.BundleURISink
}

// GetProcReceiveRegistry returns the ProcReceiveRegistry.
func (dc *Dependencies) GetProcReceiveRegistry() *gitalyhook.ProcReceiveRegistry {
	return dc.ProcReceiveRegistry
}

// GetInProgressTracker returns the ProcReceiveRegistry.
func (dc *Dependencies) GetInProgressTracker() *InProgressTracker {
	return dc.InProgressTracker
}
