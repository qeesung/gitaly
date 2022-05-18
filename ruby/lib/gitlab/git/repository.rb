require 'securerandom'

module Gitlab
  module Git
    # These are monkey patches on top of the vendored version of Repository.
    class Repository
      include Gitlab::Git::Popen
      include Gitlab::EncodingHelper
      include Gitlab::Utils::StrongMemoize

      GITALY_INTERNAL_URL = 'ssh://gitaly/internal.git'.freeze
      RUGGED_KEY = :rugged_list
      GIT_ALLOW_SHA_UPLOAD = 'uploadpack.allowAnySHA1InWant=true'.freeze

      NoRepository = Class.new(StandardError)
      InvalidRef = Class.new(StandardError)
      GitError = Class.new(StandardError)

      class << self
        def from_gitaly(gitaly_repository, call)
          new(
            gitaly_repository,
            GitalyServer.repo_path(call),
            GitalyServer.gl_repository(call),
            GitalyServer.repo_alt_dirs(call),
            GitalyServer.feature_flags(call)
          )
        end

        def from_gitaly_with_block(gitaly_repository, call)
          repository = from_gitaly(gitaly_repository, call)

          result = yield repository

          repository.cleanup

          result
        end
      end

      attr_reader :path

      # Directory name of repo
      attr_reader :name

      attr_reader :storage, :gl_repository, :gl_project_path, :relative_path

      def initialize(gitaly_repository, path, gl_repository, combined_alt_dirs = "", feature_flags = GitalyServer::FeatureFlags.new({}))
        @gitaly_repository = gitaly_repository

        @alternate_object_directories = combined_alt_dirs
                                        .split(File::PATH_SEPARATOR)
                                        .map { |d| File.join(path, d) }

        @storage = gitaly_repository.storage_name
        @relative_path = gitaly_repository.relative_path
        @path = path
        @gl_repository = gl_repository
        @gl_project_path = gitaly_repository.gl_project_path
        @feature_flags = feature_flags
      end

      def ==(other)
        [storage, relative_path] == [other.storage, other.relative_path]
      end

      attr_reader :gitaly_repository

      attr_reader :alternate_object_directories

      def sort_branches(branches, sort_by)
        case sort_by
        when 'name'
          branches.sort_by(&:name)
        when 'updated_desc'
          branches.sort do |a, b|
            b.dereferenced_target.committed_date <=> a.dereferenced_target.committed_date
          end
        when 'updated_asc'
          branches.sort do |a, b|
            a.dereferenced_target.committed_date <=> b.dereferenced_target.committed_date
          end
        else
          branches
        end
      end

      def exists?
        File.exist?(File.join(path, 'refs'))
      end

      def root_ref
        @root_ref ||= discover_default_branch
      end

      def rugged
        @rugged ||= begin
                      # Open in bare mode, for a slight performance gain
                      # https://github.com/libgit2/rugged/blob/654ff2fe12041e09707ba0647307abcb6348a7fb/ext/rugged/rugged_repo.c#L276-L278
                      Rugged::Repository.bare(path, alternates: alternate_object_directories).tap do |repo|
                        Thread.current[RUGGED_KEY] << repo if Thread.current[RUGGED_KEY]
                      end
                    end
      rescue Rugged::RepositoryError, Rugged::OSError
        raise NoRepository, 'no repository for such path'
      end

      def branch_names
        branches.map(&:name)
      end

      def branches
        branches_filter
      end

      # Git repository can contains some hidden refs like:
      #   /refs/notes/*
      #   /refs/git-as-svn/*
      #   /refs/pulls/*
      # This refs by default not visible in project page and not cloned to client side.
      def has_visible_content?
        strong_memoize(:has_visible_content) do
          branches_filter(filter: :local).any? do |ref|
            begin
              ref.name && ref.target # ensures the branch is valid

              true
            rescue Rugged::ReferenceError
              false
            end
          end
        end
      end

      def tags
        rugged.references.each("refs/tags/*").map do |ref|
          message = nil

          if ref.target.is_a?(Rugged::Tag::Annotation)
            tag_message = ref.target.message

            message = tag_message.chomp if tag_message.respond_to?(:chomp)
          end

          target_commit = Gitlab::Git::Commit.find(self, ref.target)
          Gitlab::Git::Tag.new(self,
                               name: ref.canonical_name,
                               target: ref.target,
                               target_commit: target_commit,
                               message: message)
        end.sort_by(&:name)
      end

      # Discovers the default branch based on the repository's available branches
      #
      # - If no branches are present, returns nil
      # - If one branch is present, returns its name
      # - If two or more branches are present, returns current HEAD or master or first branch
      def discover_default_branch
        names = branch_names

        return if names.empty?

        return names[0] if names.length == 1

        if rugged_head
          extracted_name = Ref.extract_branch_name(rugged_head.name)

          return extracted_name if names.include?(extracted_name)
        end

        if names.include?('master')
          'master'
        else
          names[0]
        end
      end

      def with_repo_branch_commit(start_ref)
        if empty?
          yield nil
        else
          # Directly return the commit from this repository
          yield commit(start_ref)
        end
      end

      # Directly find a branch with a simple name (e.g. master)
      #
      # force_reload causes a new Rugged repository to be instantiated
      #
      # This is to work around a bug in libgit2 that causes in-memory refs to
      # be stale/invalid when packed-refs is changed.
      # See https://gitlab.com/gitlab-org/gitlab-ce/issues/15392#note_14538333
      def find_branch(name, force_reload = false)
        reload_rugged if force_reload

        rugged_ref = rugged.ref("refs/heads/" + name)
        if rugged_ref
          target_commit = Gitlab::Git::Commit.find(self, rugged_ref.target)
          Gitlab::Git::Branch.new(self, rugged_ref.canonical_name, rugged_ref.target, target_commit)
        end
      end

      # Returns true if the given branch exists
      #
      # name - The name of the branch as a String.
      def branch_exists?(name)
        rugged.branches.exists?(name)

      # If the branch name is invalid (e.g. ".foo") Rugged will raise an error.
      # Whatever code calls this method shouldn't have to deal with that so
      # instead we just return `false` (which is true since a branch doesn't
      # exist when it has an invalid name).
      rescue Rugged::ReferenceError
        false
      end

      def merge_base(from, to)
        rugged.merge_base(from, to)
      rescue Rugged::ReferenceError
        nil
      end

      # Lookup for rugged object by oid or ref name
      def lookup(oid_or_ref_name)
        rugged.rev_parse(oid_or_ref_name)
      end

      # Return the object that +revspec+ points to.  If +revspec+ is an
      # annotated tag, then return the tag's target instead.
      def rev_parse_target(revspec)
        obj = rugged.rev_parse(revspec)
        Ref.dereference_object(obj)
      end

      def commit(ref = nil)
        ref ||= root_ref
        Gitlab::Git::Commit.find(self, ref)
      end

      def empty?
        !has_visible_content?
      end

      def cleanup
        # Opening a repository may be expensive, and we only need to close it
        # if it's been open.
        rugged&.close if defined?(@rugged)
      end

      def head_symbolic_ref
        head = rugged.ref('HEAD')

        return 'main' if head.type != :symbolic

        Ref.extract_branch_name(head.target_id)
      end

      private

      def branches_filter(filter: nil, sort_by: nil)
        branches = rugged.branches.each(filter).map do |rugged_ref|
          begin
            target_commit = Gitlab::Git::Commit.find(self, rugged_ref.target)
            Gitlab::Git::Branch.new(self, rugged_ref.canonical_name, rugged_ref.target, target_commit)
          rescue Rugged::ReferenceError
            # Omit invalid branch
          end
        end.compact

        sort_branches(branches, sort_by)
      end

      def rugged_head
        rugged.head
      rescue Rugged::ReferenceError
        nil
      end
    end
  end
end
