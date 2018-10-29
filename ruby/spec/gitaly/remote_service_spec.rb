require 'integration_helper'

describe Gitaly::RemoteService do
  include IntegrationClient
  include TestRepo

  describe 'FindRemoteRootRef' do
    subject { GitalyServer::RemoteService.new }

    it 'raises an error when request have an empty remote' do
      call = double(metadata: { 'gitaly-storage-path' => '/path/to/storage' })
      request = Gitaly::FindRemoteRootRefRequest.new(repository: gitaly_repo('default', 'foobar.git'), remote: '')

      expect { subject.find_remote_root_ref(request, call) }
        .to raise_error GRPC::InvalidArgument, /empty remote can't be queried/
    end

    it 'raises an error when remote root ref could not be found' do
      call = double(metadata: { 'gitaly-storage-path' => '/path/to/storage' })
      request = Gitaly::FindRemoteRootRefRequest.new(repository: gitaly_repo('default', 'foobar.git'), remote: 'my-remote')

      gl_projects_double = double('Gitlab::Git::GitlabProjects')
      allow(Gitlab::Git::GitlabProjects).to receive(:from_gitaly).and_return(gl_projects_double)

      expect(gl_projects_double).to receive(:find_remote_root_ref)
        .with('my-remote', ssh_key: nil, known_hosts: nil)
        .and_return(nil)

      expect { subject.find_remote_root_ref(request, call) }
        .to raise_error GRPC::Internal, /remote root ref not found for remote 'my-remote'/
    end

    it 'returns the remote root ref' do
      call = double(metadata: { 'gitaly-storage-path' => '/path/to/storage' })
      request = Gitaly::FindRemoteRootRefRequest.new(repository: gitaly_repo('default', 'foobar.git'), remote: 'my-remote')

      gl_projects_double = double('Gitlab::Git::GitlabProjects')
      allow(Gitlab::Git::GitlabProjects).to receive(:from_gitaly).and_return(gl_projects_double)

      expect(gl_projects_double).to receive(:find_remote_root_ref)
        .with('my-remote', ssh_key: nil, known_hosts: nil)
        .and_return('development')

      result = subject.find_remote_root_ref(request, call)

      expect(result.ref).to eq 'development'
    end

    context 'when request have credentials set' do
      it 'calls GitlabProjects#find_remote_root_ref with the proper ssh_key and known_hosts' do
        call = double(metadata: { 'gitaly-storage-path' => '/path/to/storage' })
        credentials = Gitaly::RepositoryCredentials.new(ssh_key: 'SSH KEY', known_hosts: 'KNOWN HOSTS')
        request = Gitaly::FindRemoteRootRefRequest.new(repository: gitaly_repo('default', 'foobar.git'), remote: 'my-remote', credentials: credentials)

        gl_projects_double = double('Gitlab::Git::GitlabProjects')
        allow(Gitlab::Git::GitlabProjects).to receive(:from_gitaly).and_return(gl_projects_double)

        expect(gl_projects_double).to receive(:find_remote_root_ref)
          .with('my-remote', ssh_key: 'SSH KEY', known_hosts: 'KNOWN HOSTS')
          .and_return('development')

        subject.find_remote_root_ref(request, call)
      end
    end
  end
end
