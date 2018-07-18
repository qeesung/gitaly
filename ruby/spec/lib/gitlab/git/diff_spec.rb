require 'spec_helper'

describe Gitlab::Git::Diff do
  describe '.filter_diff_options' do
    let(:options) { { max_files: 100, invalid_opt: true } }

    context "without default options" do
      let(:filtered_options) { described_class.filter_diff_options(options) }

      it "should filter invalid options" do
        expect(filtered_options).not_to have_key(:invalid_opt)
      end
    end

    context "with default options" do
      let(:filtered_options) do
        default_options = { max_files: 5, bad_opt: 1, ignore_whitespace_change: true }
        described_class.filter_diff_options(options, default_options)
      end

      it "should filter invalid options" do
        expect(filtered_options).not_to have_key(:invalid_opt)
        expect(filtered_options).not_to have_key(:bad_opt)
      end

      it "should merge with default options" do
        expect(filtered_options).to have_key(:ignore_whitespace_change)
      end

      it "should override default options" do
        expect(filtered_options).to have_key(:max_files)
        expect(filtered_options[:max_files]).to eq(100)
      end
    end
  end
end
