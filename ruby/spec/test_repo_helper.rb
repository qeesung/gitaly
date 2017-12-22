require 'fileutils'
require 'gitaly'

DEFAULT_STORAGE_DIR = File.expand_path('../../tmp/repositories', __FILE__)
DEFAULT_STORAGE_NAME = 'default'.freeze
TEST_REPO_PATH = File.join(DEFAULT_STORAGE_DIR, 'gitlab-test.git')

def prepare_test_repository
  return if File.directory?(TEST_REPO_PATH)

  FileUtils.mkdir_p(DEFAULT_STORAGE_DIR)
  origin = '../internal/testhelper/testdata/data/gitlab-test.git'
  clone_ok = system(*%W[git clone --quiet --bare #{origin} #{TEST_REPO_PATH}])
  abort "Failed to clone test repo. Try running 'make prepare-tests' and try again." unless clone_ok
end

prepare_test_repository

module TestRepo
  def test_repo_path
    TEST_REPO_PATH
  end

  def test_repo_gitaly
    Gitaly::Repository.new(storage_name: DEFAULT_STORAGE_NAME, relative_path: File.basename(test_repo_path))
  end
end
