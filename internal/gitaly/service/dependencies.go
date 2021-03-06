package service

import (
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	gitalyhook "gitlab.com/gitlab-org/gitaly/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/internal/storage"
)

// Dependencies assembles set of components required by different kinds of services.
type Dependencies struct {
	Cfg                config.Cfg
	RubyServer         *rubyserver.Server
	GitalyHookManager  gitalyhook.Manager
	TransactionManager transaction.Manager
	StorageLocator     storage.Locator
	ClientPool         *client.Pool
	GitCmdFactory      git.CommandFactory
}

// GetCfg return service configuration.
func (dc *Dependencies) GetCfg() config.Cfg {
	return dc.Cfg
}

// GetRubyServer returns client for the ruby processes.
func (dc *Dependencies) GetRubyServer() *rubyserver.Server {
	return dc.RubyServer
}

// GetHookManager returns hook manager.
func (dc *Dependencies) GetHookManager() gitalyhook.Manager {
	return dc.GitalyHookManager
}

// GetTxManager returns transaction manager.
func (dc *Dependencies) GetTxManager() transaction.Manager {
	return dc.TransactionManager
}

// GetLocator return storage locator.
func (dc *Dependencies) GetLocator() storage.Locator {
	return dc.StorageLocator
}

// GetConnsPool returns gRPC connection pool.
func (dc *Dependencies) GetConnsPool() *client.Pool {
	return dc.ClientPool
}

// GetGitCmdFactory return git commands factory.
func (dc *Dependencies) GetGitCmdFactory() git.CommandFactory {
	return dc.GitCmdFactory
}
