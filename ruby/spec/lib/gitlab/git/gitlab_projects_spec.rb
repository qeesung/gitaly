require 'spec_helper'

describe Gitlab::Git::GitlabProjects do
  include TestRepo

  let(:repository) { gitlab_git_from_gitaly(test_repo_read_only) }
  let(:repo_name) { 'gitlab-test.git' }
  let(:gl_projects) { build_gitlab_projects(DEFAULT_STORAGE_DIR, repo_name) }
  let(:hooks_path) { File.join(tmp_repo_path, 'hooks') }
  let(:tmp_repo_path) { TEST_REPO_PATH }
  let(:tmp_repos_path) { DEFAULT_STORAGE_DIR }

  if $VERBOSE
    let(:logger) { Logger.new(STDOUT) }
  else
    let(:logger) { double('logger').as_null_object }
  end

  def build_gitlab_projects(*args)
    described_class.new(
      *args,
      global_hooks_path: hooks_path,
      logger: logger
    )
  end

  def stub_spawn(*args, output: 'output', success: true)
    exitstatus = success ? 0 : nil

    expect(gl_projects)
      .to receive(:popen)
      .with(*args)
      .and_return([output, exitstatus])
  end

  def stub_spawn_timeout(*args, output: 'output', success: true)
    exitstatus = success ? 0 : nil

    expect(gl_projects)
      .to receive(:popen_with_timeout)
      .with(*args)
      .and_return([output, exitstatus])
  end

  describe '#initialize' do
    it { expect(gl_projects.shard_path).to eq(tmp_repos_path) }
    it { expect(gl_projects.repository_relative_path).to eq(repo_name) }
    it { expect(gl_projects.repository_absolute_path).to eq(File.join(tmp_repos_path, repo_name)) }
    it { expect(gl_projects.logger).to eq(logger) }
  end

  describe '#push_branches' do
    let(:remote_name) { 'remote-name' }
    let(:branch_name) { 'master' }
    let(:cmd) { %W(#{Gitlab.config.git.bin_path} push -- #{remote_name} #{branch_name}) }
    let(:force) { false }

    subject { gl_projects.push_branches(remote_name, 600, force, [branch_name]) }

    it 'executes the command' do
      stub_spawn_timeout(cmd, 600, tmp_repo_path, success: true)

      is_expected.to be_truthy
    end

    it 'fails' do
      stub_spawn_timeout(cmd, 600, tmp_repo_path, success: false)

      is_expected.to be_falsy
    end

    context 'with --force' do
      let(:cmd) { %W(#{Gitlab.config.git.bin_path} push --force -- #{remote_name} #{branch_name}) }
      let(:force) { true }

      it 'executes the command' do
        stub_spawn_timeout(cmd, 600, tmp_repo_path, success: true)

        is_expected.to be_truthy
      end
    end
  end

  describe '#fetch_remote' do
    let(:remote_name) { 'remote-name' }
    let(:branch_name) { 'master' }
    let(:force) { false }
    let(:prune) { true }
    let(:tags) { true }
    let(:args) { { force: force, tags: tags, prune: prune }.merge(extra_args) }
    let(:extra_args) { {} }
    let(:cmd) { %W(#{Gitlab.config.git.bin_path} fetch #{remote_name} --quiet --prune --tags) }

    subject { gl_projects.fetch_remote(remote_name, 600, args) }

    def stub_tempfile(name, filename, opts = {})
      chmod = opts.delete(:chmod)
      file = StringIO.new

      allow(file).to receive(:close!)
      allow(file).to receive(:path).and_return(name)

      expect(Tempfile).to receive(:new).with(filename).and_return(file)
      expect(file).to receive(:chmod).with(chmod) if chmod

      file
    end

    context 'with default args' do
      it 'executes the command' do
        stub_spawn_timeout(cmd, 600, tmp_repo_path, {}, success: true)

        is_expected.to be_truthy
      end

      it 'returns false if the command fails' do
        stub_spawn_timeout(cmd, 600, tmp_repo_path, {}, success: false)

        is_expected.to be_falsy
      end
    end

    context 'with --force' do
      let(:force) { true }
      let(:cmd) { %W(#{Gitlab.config.git.bin_path} fetch #{remote_name} --quiet --prune --force --tags) }

      it 'executes the command with forced option' do
        stub_spawn_timeout(cmd, 600, tmp_repo_path, {}, success: true)

        is_expected.to be_truthy
      end
    end

    context 'with --no-tags' do
      let(:tags) { false }
      let(:cmd) { %W(#{Gitlab.config.git.bin_path} fetch #{remote_name} --quiet --prune --no-tags) }

      it 'executes the command' do
        stub_spawn_timeout(cmd, 600, tmp_repo_path, {}, success: true)

        is_expected.to be_truthy
      end
    end

    context 'with no prune' do
      let(:prune) { false }
      let(:cmd) { %W(#{Gitlab.config.git.bin_path} fetch #{remote_name} --quiet --tags) }

      it 'executes the command' do
        stub_spawn_timeout(cmd, 600, tmp_repo_path, {}, success: true)

        is_expected.to be_truthy
      end
    end

    describe 'with an SSH key' do
      let(:extra_args) { { ssh_key: 'SSH KEY' } }

      it 'sets GIT_SSH to a custom script' do
        script = stub_tempfile('scriptFile', 'gitlab-shell-ssh-wrapper', chmod: 0o755)
        key = stub_tempfile('/tmp files/keyFile', 'gitlab-shell-key-file', chmod: 0o400)

        stub_spawn_timeout(cmd, 600, tmp_repo_path, { 'GIT_SSH' => 'scriptFile' }, success: true)

        is_expected.to be_truthy

        expect(script.string).to eq("#!/bin/sh\nexec ssh '-oIdentityFile=\"/tmp files/keyFile\"' '-oIdentitiesOnly=\"yes\"' \"$@\"")
        expect(key.string).to eq('SSH KEY')
      end
    end

    describe 'with known_hosts data' do
      let(:extra_args) { { known_hosts: 'KNOWN HOSTS' } }

      it 'sets GIT_SSH to a custom script' do
        script = stub_tempfile('scriptFile', 'gitlab-shell-ssh-wrapper', chmod: 0o755)
        key = stub_tempfile('/tmp files/knownHosts', 'gitlab-shell-known-hosts', chmod: 0o400)

        stub_spawn_timeout(cmd, 600, tmp_repo_path, { 'GIT_SSH' => 'scriptFile' }, success: true)

        is_expected.to be_truthy

        expect(script.string).to eq("#!/bin/sh\nexec ssh '-oStrictHostKeyChecking=\"yes\"' '-oUserKnownHostsFile=\"/tmp files/knownHosts\"' \"$@\"")
        expect(key.string).to eq('KNOWN HOSTS')
      end
    end
  end

  describe '#find_remote_root_ref' do
    let(:remote_name) { 'remote-name' }
    let(:cmd) { %W(#{Gitlab.config.git.bin_path} remote show #{remote_name}) }

    subject { gl_projects.find_remote_root_ref(remote_name) }

    it 'returns remote root ref when succeeds' do
      output = <<~OUTPUT
        * remote origin
        Fetch URL: https://gitlab.com/gitlab-org/gitlab-ce.git
        Push  URL: https://gitlab.com/gitlab-org/gitlab-ce.git
        HEAD branch: development
      OUTPUT

      stub_spawn(cmd, tmp_repo_path, output: output, success: true)

      is_expected.to eq 'development'
    end

    it 'returns nil when fails' do
      stub_spawn(cmd, tmp_repo_path, success: false)

      is_expected.to be_nil
    end
  end
end
