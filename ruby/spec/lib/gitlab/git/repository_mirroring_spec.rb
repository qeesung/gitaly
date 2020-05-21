# frozen_string_literal: true

require 'spec_helper'

describe Gitlab::Git::RepositoryMirroring do
  class FakeRepository
    include Gitlab::Git::RepositoryMirroring

    attr_reader :rugged

    def initialize(projects_stub, rugged_instance = nil)
      @gitlab_projects = projects_stub
      @rugged = rugged_instance
    end

    def gitlab_projects_error
      raise Gitlab::Git::CommandError, @gitlab_projects.output
    end
  end

  describe '#remote_branches' do
    let(:projects_stub) { double.as_null_object }
    let(:rugged_stub) { double.as_null_object }

    subject(:repository) { FakeRepository.new(projects_stub, rugged_stub) }

    it 'passes environment to `ls-remote`' do
      env = { option_a: true, option_b: false }

      allow(repository).to receive(:feature_enabled?)
        .with(:remote_branches_ls_remote)
        .and_return(true)
      expect(repository).to receive(:list_remote_refs)
        .with('remote_a', env: env)
        .and_return([])

      repository.remote_branches('remote_a', env: env)
    end
  end

  describe '#push_remote_branches' do
    let(:projects_stub) { double.as_null_object }

    subject(:repository) { FakeRepository.new(projects_stub) }

    context 'with a successful first push' do
      it 'returns true' do
        expect(projects_stub).to receive(:push_branches)
          .with('remote_a', anything, true, %w[master], env: {})
          .once
          .and_return(true)

        expect(projects_stub).not_to receive(:output)

        expect(repository.push_remote_branches('remote_a', %w[master])).to eq(true)
      end
    end

    context 'with a failed push' do
      it 'raises an error' do
        output = "Oh no, push mirroring failed!"
        allow(projects_stub).to receive(:output).and_return(output)

        expect(projects_stub).to receive(:push_branches).and_return(false)

        expect { repository.push_remote_branches('remote_a', %w[master develop]) }
          .to raise_error(Gitlab::Git::CommandError, output)
      end
    end
  end
end
