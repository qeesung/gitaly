# GITLAB_SHELL_DIR has been deprecated
require 'pathname'

ROOT_PATH = Pathname.new(ENV['GITALY_GITLAB_SHELL_DIR'] || ENV['GITLAB_SHELL_DIR'] || File.expand_path('..', __dir__)).freeze
LOG_PATH = Pathname.new(ENV.fetch('GITALY_LOG_DIR', ROOT_PATH))
LOG_LEVEL = ENV.fetch('GITALY_LOG_LEVEL', 'INFO')
LOG_FORMAT = ENV.fetch('GITALY_LOG_FORMAT', 'text')

# We are transitioning parts of gitlab-shell into the gitaly project. In
# gitaly, GITALY_EMBEDDED will be true.
GITALY_EMBEDDED = true

require_relative 'gitlab_config'
