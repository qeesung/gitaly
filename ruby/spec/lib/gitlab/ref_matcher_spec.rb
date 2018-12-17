require 'spec_helper'

describe GitLab::RefMatcher do
  subject { described_class.new(pattern) }

  describe "#matches?" do
    context "when the ref pattern is not a wildcard" do
      let(:pattern) { 'production/some-branch' }

      it "returns true for branch names that are an exact match" do
        expect(subject.matches?('production/some-branch')).to be true
      end

      it "returns false for branch names that are not an exact match" do
        expect(subject.matches?("staging/some-branch")).to be false
      end
    end

    context "when the ref pattern  name contains wildcard(s)" do
      context "when there is a single '*'" do
        let(:pattern) { 'production/*' }

        it "returns true for branch names matching the wildcard" do
          expect(subject.matches?("production/some-branch")).to be true
          expect(subject.matches?("production/")).to be true
        end

        it "returns false for branch names not matching the wildcard" do
          expect(subject.matches?("staging/some-branch")).to be false
          expect(subject.matches?("production")).to be false
        end
      end

      context "when the wildcard contains regex symbols other than a '*'" do
        let(:pattern) { "pro.duc.tion/*" }

        it "returns true for branch names matching the wildcard" do
          expect(subject.matches?("pro.duc.tion/some-branch")).to be true
        end

        it "returns false for branch names not matching the wildcard" do
          expect(subject.matches?("production/some-branch")).to be false
          expect(subject.matches?("proXducYtion/some-branch")).to be false
        end
      end

      context "when there are '*'s at either end" do
        let(:pattern) { "*/production/*" }

        it "returns true for branch names matching the wildcard" do
          expect(subject.matches?("gitlab/production/some-branch")).to be true
          expect(subject.matches?("/production/some-branch")).to be true
          expect(subject.matches?("gitlab/production/")).to be true
          expect(subject.matches?("/production/")).to be true
        end

        it "returns false for branch names not matching the wildcard" do
          expect(subject.matches?("gitlabproductionsome-branch")).to be false
          expect(subject.matches?("production/some-branch")).to be false
          expect(subject.matches?("gitlab/production")).to be false
          expect(subject.matches?("production")).to be false
        end
      end

      context "when there are arbitrarily placed '*'s" do
        let(:pattern) { "pro*duction/*/gitlab/*" }

        it "returns true for branch names matching the wildcard" do
          expect(subject.matches?("production/some-branch/gitlab/second-branch")).to be true
          expect(subject.matches?("proXYZduction/some-branch/gitlab/second-branch")).to be true
          expect(subject.matches?("proXYZduction/gitlab/gitlab/gitlab")).to be true
          expect(subject.matches?("proXYZduction//gitlab/")).to be true
          expect(subject.matches?("proXYZduction/some-branch/gitlab/")).to be true
          expect(subject.matches?("proXYZduction//gitlab/some-branch")).to be true
        end

        it "returns false for branch names not matching the wildcard" do
          expect(subject.matches?("production/some-branch/not-gitlab/second-branch")).to be false
          expect(subject.matches?("prodXYZuction/some-branch/gitlab/second-branch")).to be false
          expect(subject.matches?("proXYZduction/gitlab/some-branch/gitlab")).to be false
          expect(subject.matches?("proXYZduction/gitlab//")).to be false
          expect(subject.matches?("proXYZduction/gitlab/")).to be false
          expect(subject.matches?("proXYZduction//some-branch/gitlab")).to be false
        end
      end
    end
  end
end
