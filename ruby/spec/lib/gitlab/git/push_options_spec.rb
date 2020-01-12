# frozen_string_literal: true

require 'spec_helper'

describe Gitlab::Git::PushOptions do
  subject { described_class.new }

  describe '#env_data' do
    it 'produces GIT_PUSH_OPTION environment variables' do
      subject.add_option('mr.create')
      subject.enable_ci_skip

      env_data = subject.env_data
      expect(env_data.count).to eq(3)
      expect(env_data['GIT_PUSH_OPTION_COUNT']).to eq(2)
      expect(env_data['GIT_PUSH_OPTION_0']).to eq('mr.create')
      expect(env_data['GIT_PUSH_OPTION_1']).to eq('ci.skip')
    end
  end
end
