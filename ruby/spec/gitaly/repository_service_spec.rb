require 'integration_helper'

describe Gitaly::RepositoryService do
  include IntegrationClient
  include TestRepo

  let(:service_stub) { gitaly_stub(:RepositoryService) }

  describe 'RepositoryExists' do
    it 'returns false if the repository does not exist' do
      request = Gitaly::RepositoryExistsRequest.new(repository: gitaly_repo('default', 'foobar.git'))
      response = service_stub.repository_exists(request)
      expect(response.exists).to eq(false)
    end

    it 'returns true if the repository exists' do
      request = Gitaly::RepositoryExistsRequest.new(repository: test_repo_gitaly)
      response = service_stub.repository_exists(request)
      expect(response.exists).to eq(true)
    end
  end
end
