require 'gitaly'

require_relative 'gitlab/git.rb'

require_relative 'gitaly_server/utils.rb'
require_relative 'gitaly_server/commit_service.rb'
require_relative 'gitaly_server/diff_service.rb'
require_relative 'gitaly_server/ref_service.rb'
require_relative 'gitaly_server/operations_service.rb'
require_relative 'gitaly_server/repository_service.rb'

module GitalyServer
  REPO_PATH_HEADER = 'gitaly-repo-path'.freeze

  def self.repo_path(_call)
    _call.metadata.fetch(REPO_PATH_HEADER)
  end

  def self.register_handlers(server)
    server.handle(CommitService.new)
    server.handle(DiffService.new)
    server.handle(RefService.new)
    server.handle(OperationsService.new)
    server.handle(RepositoryService.new)
  end
end
