module Gitlab
  module Git
    class Diff
      class << self
        # Return a copy of the +options+ hash containing only keys that can be
        # passed to Rugged.  Allowed options are:
        #
        #  :ignore_whitespace_change ::
        #    If true, changes in amount of whitespace will be ignored.
        #
        #  :disable_pathspec_match ::
        #    If true, the given +*paths+ will be applied as exact matches,
        #    instead of as fnmatch patterns.
        #
        def filter_diff_options(options, default_options = {})
          allowed_options = [:ignore_whitespace_change,
                             :disable_pathspec_match, :paths,
                             :max_files, :max_lines, :limits, :expanded]

          if default_options
            actual_defaults = default_options.dup
            actual_defaults.keep_if do |key|
              allowed_options.include?(key)
            end
          else
            actual_defaults = {}
          end

          if options
            filtered_opts = options.dup
            filtered_opts.keep_if do |key|
              allowed_options.include?(key)
            end
            filtered_opts = actual_defaults.merge(filtered_opts)
          else
            filtered_opts = actual_defaults
          end

          filtered_opts
        end
      end
    end
  end
end
