# frozen_string_literal: true

require 'spec_helper'

describe GitalyServer::FeatureFlags do
  describe '#enabled?' do
    let(:metadata) do
      {
        "#{described_class::HEADER_PREFIX}some-feature" => 'true',
        'gitaly-storage-path' => 'foo',
        'gitaly-repo-path' => 'bar'
      }
    end

    subject { described_class.new(metadata) }

    it 'returns true for an enabled flag' do
      expect(subject.enabled?(:some_feature)).to eq(true)
    end

    it 'returns false for an unknown flag' do
      expect(subject.enabled?(:missing_feature)).to eq(false)
    end

    it 'removes the prefix if provided' do
      expect(subject.enabled?(metadata.keys.first)).to eq(true)
    end

    it 'translates underscores' do
      expect(subject.enabled?('some-feature')).to eq(true)
    end
  end

  describe '#disabled?' do
    it 'is the inverse of `enabled?`' do
      instance = described_class.new({})

      expect(instance).to receive(:enabled?)
        .with(:some_feature)
        .and_return(false)

      expect(instance.disabled?(:some_feature)).to eq(true)
    end
  end
end
