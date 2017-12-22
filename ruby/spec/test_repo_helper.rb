require 'fileutils'
require 'securerandom'
require 'gitaly'
require 'rugged'

DEFAULT_STORAGE_DIR = File.expand_path('../../tmp/repositories', __FILE__)
DEFAULT_STORAGE_NAME = 'default'.freeze
TEST_REPO_PATH = File.join(DEFAULT_STORAGE_DIR, 'gitlab-test.git')
TEST_REPO_ORIGIN = '../internal/testhelper/testdata/data/gitlab-test.git'.freeze

module TestRepo
  def self.prepare_test_repository
    FileUtils.rm_rf(Dir["#{DEFAULT_STORAGE_DIR}/mutable-*"])
    return if File.directory?(TEST_REPO_PATH)

    FileUtils.mkdir_p(DEFAULT_STORAGE_DIR)
    clone_new_repo!(TEST_REPO_PATH)
  end

  def test_repo_read_only
    Gitaly::Repository.new(storage_name: DEFAULT_STORAGE_NAME, relative_path: File.basename(TEST_REPO_PATH))
  end

  def new_mutable_test_repo
    relative_path = "mutable-#{SecureRandom.hex(6)}.git"
    TestRepo.clone_new_repo!(File.join(DEFAULT_STORAGE_DIR, relative_path))
    Gitaly::Repository.new(storage_name: DEFAULT_STORAGE_NAME, relative_path: relative_path)
  end

  def rugged_from_gitaly(gitaly_repo)
    Rugged::Repository.new(repo_path_from_gitaly(gitaly_repo))
  end

  def repo_path_from_gitaly(gitaly_repo)
    storage_name = gitaly_repo.storage_name
    raise "this helper does not know storage #{storage_name.inspect}" unless storage_name == DEFAULT_STORAGE_NAME
    File.join(DEFAULT_STORAGE_DIR, gitaly_repo.relative_path)
  end

  def self.clone_new_repo!(destination)
    return if system(*%W[git clone --quiet --bare #{TEST_REPO_ORIGIN} #{destination}])
    abort "Failed to clone test repo. Try running 'make prepare-tests' and try again."
  end
end

TestRepo.prepare_test_repository
