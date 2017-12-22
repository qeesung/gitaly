require 'gitaly'

$socket_path = 'gitaly.socket'

def start_gitaly
  build_dir = File.expand_path('../../../_build', __FILE__)
  tmp_dir = File.expand_path('../../tmp', __FILE__)
  storage_dir = File.join(tmp_dir, 'repositories')
  gitlab_shell_dir = File.join(tmp_dir, 'gitlab-shell')
  
  FileUtils.mkdir_p(tmp_dir)
  FileUtils.mkdir_p(File.join(gitlab_shell_dir, 'hooks'))
  
  config_toml = <<EOS
socket_path = "#{$socket_path}"
bin_dir = "#{build_dir}/bin"

[gitlab-shell]
dir = "#{gitlab_shell_dir}"

[gitaly-ruby]
dir = "#{build_dir}/assembly/ruby"

[[storage]]
name = "default"
path = "#{storage_dir}"
EOS
  config_path = File.join(tmp_dir, 'gitaly-rspec-config.toml')
  File.write(config_path, config_toml)
  
  test_log = File.join(tmp_dir, 'gitaly-rspec-test.log')
  options = { out: test_log, err: test_log, chdir: tmp_dir }
  gitaly_pid = spawn(File.join(build_dir, 'bin/gitaly'), config_path, options)
  at_exit { Process.kill('KILL', gitaly_pid) }
end

start_gitaly
