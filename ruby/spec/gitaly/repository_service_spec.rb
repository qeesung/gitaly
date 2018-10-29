require 'integration_helper'

describe Gitaly::RepositoryService do
  include IntegrationClient
  include TestRepo

  describe 'RepositoryExists' do
    subject { gitaly_stub(:RepositoryService) }

    it 'returns false if the repository does not exist' do
      request = Gitaly::RepositoryExistsRequest.new(repository: gitaly_repo('default', 'foobar.git'))
      response = subject.repository_exists(request)
      expect(response.exists).to eq(false)
    end

    it 'returns true if the repository exists' do
      request = Gitaly::RepositoryExistsRequest.new(repository: test_repo_read_only)
      response = subject.repository_exists(request)
      expect(response.exists).to eq(true)
    end
  end

  describe 'FetchRemote' do
    subject { GitalyServer::RepositoryService.new }

    context 'request does not have ssh_key and known_hosts set' do
      it 'calls GitlabProjects#fetch_remote with nil ssh_key and known_hosts' do
        call = double(metadata: { 'gitaly-storage-path' => '/path/to/storage' })
        request = Gitaly::FetchRemoteRequest.new(repository: gitaly_repo('default', 'foobar.git'), remote: 'my-remote')

        gl_projects_double = double('Gitlab::Git::GitlabProjects')
        allow(Gitlab::Git::GitlabProjects).to receive(:from_gitaly).and_return(gl_projects_double)

        expect(gl_projects_double).to receive(:fetch_remote)
          .with('my-remote', 0,
                force: false,
                tags: true,
                ssh_key: nil,
                known_hosts: nil)
          .and_return(true)

        GitalyServer::RepositoryService.new.fetch_remote(request, call)
      end
    end

    context 'request have ssh_key and known_hosts set' do
      it 'calls GitlabProjects#fetch_remote with the proper ssh_key and known_hosts' do
        call = double(metadata: { 'gitaly-storage-path' => '/path/to/storage' })
        request = Gitaly::FetchRemoteRequest.new(repository: gitaly_repo('default', 'foobar.git'), remote: 'my-remote', ssh_key: 'SSH KEY', known_hosts: 'KNOWN HOSTS')

        gl_projects_double = double('Gitlab::Git::GitlabProjects')
        allow(Gitlab::Git::GitlabProjects).to receive(:from_gitaly).and_return(gl_projects_double)

        expect(gl_projects_double).to receive(:fetch_remote)
          .with('my-remote', 0,
                force: false,
                tags: true,
                ssh_key: 'SSH KEY',
                known_hosts: 'KNOWN HOSTS')
          .and_return(true)

        GitalyServer::RepositoryService.new.fetch_remote(request, call)
      end
    end

    context 'when request have credentials set' do
      it 'calls GitlabProjects#fetch_remote with the proper ssh_key and known_hosts' do
        call = double(metadata: { 'gitaly-storage-path' => '/path/to/storage' })
        credentials = Gitaly::RepositoryCredentials.new(ssh_key: 'SSH KEY', known_hosts: 'KNOWN HOSTS')
        request = Gitaly::FetchRemoteRequest.new(repository: gitaly_repo('default', 'foobar.git'), remote: 'my-remote', credentials: credentials)

        gl_projects_double = double('Gitlab::Git::GitlabProjects')
        allow(Gitlab::Git::GitlabProjects).to receive(:from_gitaly).and_return(gl_projects_double)

        expect(gl_projects_double).to receive(:fetch_remote)
          .with('my-remote', 0,
                force: false,
                tags: true,
                ssh_key: 'SSH KEY',
                known_hosts: 'KNOWN HOSTS')
          .and_return(true)

        subject.fetch_remote(request, call)
      end
    end
  end
end
