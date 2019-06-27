require 'yaml'

class GitlabConfig
  attr_reader :config

  def initialize
    @config = YAML.load_file(File.join(ROOT_PATH, 'config.yml'))
  end

  def secret_file
    @config['secret_file'] ||= File.join(ROOT_PATH, '.gitlab_shell_secret')
  end

  # Pass a default value because this is called from a repo's context; in which
  # case, the repo's hooks directory should be the default.
  #
  def custom_hooks_dir
    @config['custom_hooks_dir'] || File.join(ROOT_PATH, 'hooks')
  end

  def gitlab_url
    (@config['gitlab_url'] ||= "http://localhost:8080").sub(%r{/*$}, '')
  end

  def http_settings
    @config['http_settings'] ||= {}
  end

  def log_file
    @config['log_file'] ||= File.join(ROOT_PATH, 'gitlab-shell.log')
  end

  def log_level
    @config['log_level'] ||= 'INFO'
  end

  def log_format
    @config['log_format'] ||= 'text'
  end

  def metrics_log_file
    @config['metrics_log_file'] ||= File.join(ROOT_PATH, 'gitlab-shell-metrics.log')
  end
end
