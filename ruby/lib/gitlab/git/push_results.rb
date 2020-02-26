# frozen_string_literal: true

module Gitlab
  module Git
    # Parses the output of a `git push --porcelain` command
    class PushResults
      attr_reader :all

      def initialize(raw_output)
        # If --porcelain is used, then each line of the output is of the form:
        #     <flag> \t <from>:<to> \t <summary> (<reason>)
        #
        # See https://git-scm.com/docs/git-push#_output
        # and https://github.com/git/git/blob/v2.25.1/transport.c#L466-L475
        @all = raw_output.each_line.map do |line|
          line.chomp!

          fields = line.split("\t", 3)

          # Sanity check for porcelain output
          next unless fields.size == 3

          flag = fields.shift
          next unless Result.valid_flag?(flag)

          from, to = fields.shift.split(':')
          summary = fields.shift

          Result.new(flag, from, to, summary)
        end.compact
      end

      # Returns an Array of branch names that were not rejected nor up-to-date
      def accepted_branches
        all.select(&:accepted?).collect(&:branch_name)
      end

      # Returns an Array of branch names that were rejected
      def rejected_branches
        all.select(&:rejected?).collect(&:branch_name)
      end

      Result = Struct.new(:flag_char, :from, :to, :summary) do
        # A single character indicating the status of the ref
        FLAGS = {
          ' ' => :fast_forward, # (space) for a successfully pushed fast-forward;
          '+' => :forced,       # +       for a successful forced update;
          '-' => :deleted,      # -       for a successfully deleted ref;
          '*' => :new,          # *       for a successfully pushed new ref;
          '!' => :rejected,     # !       for a ref that was rejected or failed to push; and
          '=' => :up_to_date    # =       for a ref that was up to date and did not need pushing.
        }.freeze

        def self.valid_flag?(flag)
          FLAGS.key?(flag)
        end

        def flag
          FLAGS[flag_char]
        end

        def rejected?
          flag == :rejected
        end

        def accepted?
          !rejected? && flag != :up_to_date
        end

        def branch_name
          to.delete_prefix('refs/heads/')
        end
      end
    end
  end
end
