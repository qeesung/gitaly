# frozen_string_literal: true

require 'spec_helper'

describe Gitlab::Git::PushOptions do
  subject { described_class.new(options) }

  describe '#env_data' do
    context 'when push options are set' do
      let(:options) { ['ci.skip', 'test=value'] }

      it 'sets GIT_PUSH_OPTION environment variables' do
        env_data = subject.env_data

        expect(env_data.count).to eq(3)
        expect(env_data['GIT_PUSH_OPTION_COUNT']).to eq('2')
        expect(env_data['GIT_PUSH_OPTION_0']).to eq('ci.skip')
        expect(env_data['GIT_PUSH_OPTION_1']).to eq('test=value')
      end
    end

    context 'when push options are not set' do
      let(:options) { [] }

      it 'does not set any variable' do
        env_data = subject.env_data

        expect(env_data).to eq({})
      end
    end
  end
end
