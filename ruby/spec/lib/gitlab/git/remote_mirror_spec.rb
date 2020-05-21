require 'spec_helper'

describe Gitlab::Git::RemoteMirror do
  include TestRepo

  let(:repository) { gitlab_git_from_gitaly_with_gitlab_projects(new_mutable_test_repo) }
  let(:gitlab_projects) { repository.gitlab_projects }
  let(:ref_name) { 'remote' }
  let(:ssh_key) { 'SSH KEY' }
  let(:known_hosts) { 'KNOWN HOSTS' }
  let(:ssh_auth) { Gitlab::Git::SshAuth.new(ssh_key, known_hosts) }
  let(:gl_projects_timeout) { Gitlab::Git::RepositoryMirroring::GITLAB_PROJECTS_TIMEOUT }
  let(:gl_projects_force) { true }
  let(:env) { { 'GIT_SSH_COMMAND' => /ssh/ } }

  subject(:remote_mirror) do
    described_class.new(
      repository,
      ref_name,
      ssh_auth: ssh_auth,
      only_branches_matching: [],
      keep_divergent_refs: false
    )
  end

  def ref(name)
    double("ref-#{name}", name: name, dereferenced_target: double(id: name))
  end

  def tag(name)
    Gitlab::Git::Tag.new(nil, name: "refs/tags/#{name}", target_commit: double(id: name))
  end

  describe '#update' do
    context 'with wildcard protected branches' do
      subject(:remote_mirror) do
        described_class.new(
          repository,
          ref_name,
          ssh_auth: ssh_auth,
          only_branches_matching: ['master', '*-stable'],
          keep_divergent_refs: false
        )
      end

      it 'updates the remote repository' do
        # Stub this check so we try to delete the obsolete tag
        allow(repository).to receive(:ancestor?).and_return(true)

        expect(repository).to receive(:local_branches).and_return([ref('master'), ref('11-5-stable'), ref('unprotected')])
        expect(repository).to receive(:remote_branches)
          .with(ref_name, env: env)
          .and_return([ref('master'), ref('obsolete-branch')])

        expect(repository).to receive(:tags).and_return([tag('v1.0.0'), tag('new-tag')])
        expect(repository).to receive(:remote_tags)
          .with(ref_name, env: env)
          .and_return([tag('v1.0.0'), tag('obsolete-tag')])

        expect(gitlab_projects)
          .to receive(:push_branches)
          .with(ref_name, gl_projects_timeout, gl_projects_force, ['master', '11-5-stable'], env: env)
          .and_return(true)

        expect(gitlab_projects)
          .to receive(:push_branches)
          .with(ref_name, gl_projects_timeout, gl_projects_force, ['refs/tags/v1.0.0', 'refs/tags/new-tag'], env: env)
          .and_return(true)

        # Leave remote branches that do not match the protected branch filter
        expect(gitlab_projects)
          .not_to receive(:delete_remote_branches)
          .with(ref_name, ['obsolete-branch'], env: env)

        expect(gitlab_projects)
          .to receive(:delete_remote_branches)
          .with(ref_name, ['refs/tags/obsolete-tag'], env: env)
          .and_return(true)

        remote_mirror.update
      end
    end

    it 'updates the remote repository' do
      # Stub this check so we try to delete the obsolete refs
      allow(repository).to receive(:ancestor?).and_return(true)

      expect(repository).to receive(:local_branches).and_return([ref('master'), ref('new-branch')])
      expect(repository).to receive(:remote_branches)
        .with(ref_name, env: env)
        .and_return([ref('master'), ref('obsolete-branch')])

      expect(repository).to receive(:tags).and_return([tag('v1.0.0'), tag('new-tag')])
      expect(repository).to receive(:remote_tags)
        .with(ref_name, env: env)
        .and_return([tag('v1.0.0'), tag('obsolete-tag')])

      expect(gitlab_projects)
        .to receive(:push_branches)
        .with(ref_name, gl_projects_timeout, gl_projects_force, ['master', 'new-branch'], env: env)
        .and_return(true)

      expect(gitlab_projects)
        .to receive(:push_branches)
        .with(ref_name, gl_projects_timeout, gl_projects_force, ['refs/tags/v1.0.0', 'refs/tags/new-tag'], env: env)
        .and_return(true)

      expect(gitlab_projects)
        .to receive(:delete_remote_branches)
        .with(ref_name, ['obsolete-branch'], env: env)
        .and_return(true)

      expect(gitlab_projects)
        .to receive(:delete_remote_branches)
        .with(ref_name, ['refs/tags/obsolete-tag'], env: env)
        .and_return(true)

      remote_mirror.update
    end
  end
end
