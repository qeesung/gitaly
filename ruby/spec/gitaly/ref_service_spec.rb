require 'integration_helper'
require 'securerandom'

describe Gitaly::RefService do
  include IntegrationClient
  include TestRepo

  let(:service_stub) { gitaly_stub(:RefService) }

  describe 'CreateBranch' do
    it 'can create a branch' do
      branch_name = 'branch-' + SecureRandom.hex(10)
      request = Gitaly::CreateBranchRequest.new(
        repository: test_repo_mutable,
        name: branch_name,
        start_point: 'master'
      )
      response = service_stub.create_branch(request)

      expect(response.status).to eq(:OK)
    end
  end
end
