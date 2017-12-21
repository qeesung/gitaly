require 'spec_helper'

describe Gitlab::Config do
  describe '#gitlab_shell' do
    subject { described_class.new.gitlab_shell }

    let(:gitlab_shell_path) { '/foo/bar/gitlab-shell' }

    before do
      ENV['GITALY_RUBY_GITLAB_SHELL_PATH'] = gitlab_shell_path
    end

    after do
      ENV.delete('GITALY_RUBY_GITLAB_SHELL_PATH')
    end

    it { expect(subject.path).to eq(gitlab_shell_path) }
    it { expect(subject.hooks_path).to eq(File.join(gitlab_shell_path, 'hooks')) }
  end
end
