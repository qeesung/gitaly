require_relative 'spec_helper'
require_relative '../lib/gitlab_config'
require 'json'

describe GitlabConfig do
  let(:config) { GitlabConfig.new }

  context "without ENV['GITALY_GITLAB_SHELL_CONFIG'] is passed in" do
    let(:config_data) { {} }

    before { expect(YAML).to receive(:load_file).and_return(config_data) }

    describe '#gitlab_url' do
      let(:url) { 'http://test.com' }

      subject { config.gitlab_url }

      before { config_data['gitlab_url'] = url }

      it { is_expected.not_to be_empty }
      it { is_expected.to eq(url) }

      context 'remove trailing slashes' do
        before { config_data['gitlab_url'] = url + '//' }

        it { is_expected.to eq(url) }
      end
    end

    describe '#secret_file' do
      subject { config.secret_file }

      it 'returns ".gitlab_shell_secret" by default' do
        is_expected.to eq(File.join(File.expand_path('..', __dir__),'.gitlab_shell_secret'))
      end
    end

    describe '#fetch_from_legacy_config' do
      let(:key) { 'yaml_key' }

      where(:yaml_value, :default, :expected_value) do
        [
          ['a', 'b', 'a'],
          [nil, 'b', 'b'],
          ['a', nil, 'a'],
          [nil, {}, {}]
        ]
      end

      with_them do
        it 'returns the correct value' do
          config_data[key] = yaml_value

          expect(config.fetch_from_legacy_config(key, default)).to eq(expected_value)
        end
      end
    end
  end

  context "when ENV['GITALY_GITLAB_SHELL_CONFIG'] is passed in" do
    let(:config_data) { {'secret_file': 'path/to/secret/file',
      'custom_hooks_dir': '/path/to/custom_hooks',
      'gitlab_url': 'http://localhost:123454',
      'http_settings': {'user': 'user_123', 'password':'password123', 'ca_file': '/path/to/ca_file', 'ca_path': 'path/to/ca_path'},
      'log_path': '/path/to/log',
      'log_level': 'myloglevel',
      'log_format': 'log_format'} }

    before { allow(ENV).to receive(:fetch).with('GITALY_GITLAB_SHELL_CONFIG', '{}').and_return(config_data.to_json) }

    describe '#secret_file' do
      it 'returns the correct secret_file' do
        expect(config.secret_file).to eq(config_data[:secret_file])
      end
    end

    describe '#custom_hooks_dir' do
      it 'returns the correct custom_hooks_dir' do
        expect(config.custom_hooks_dir).to eq(config_data[:custom_hooks_dir])
      end
    end

    describe '#http_settings' do
      it 'returns the correct http_settings' do
        expect(config.http_settings).to eq(config_data[:http_settings].transform_keys(&:to_s))
      end
    end

    describe '#gitlab_url' do
      it 'returns the correct gitlab_url' do
        expect(config.gitlab_url).to eq(config_data[:gitlab_url])
      end
    end

    describe '#log_path' do
      it 'returns the correct log_path' do
        expect(config.log_file).to eq(Pathname.new(File.join(config_data[:log_path], 'gitlab-shell.log')))
      end
    end

    describe '#log_level' do
      it 'returns the correct log_level' do
        expect(config.log_level).to eq(config_data[:log_level])
      end
    end

    describe '#log_format' do
      it 'returns the correct log_format' do
        expect(config.log_format).to eq(config_data[:log_format])
      end
    end
  end
end
