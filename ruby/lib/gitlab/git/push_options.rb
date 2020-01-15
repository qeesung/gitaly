# frozen_string_literal: true

module Gitlab
  module Git
    class PushOptions
      attr_accessor :options

      def initialize(options = [])
        @options = options
      end

      def env_data
        return {} if options.empty?

        data = {
          'GIT_PUSH_OPTION_COUNT' => options.count.to_s
        }

        options.each_with_index do |opt, index|
          data["GIT_PUSH_OPTION_#{index}"] = opt
        end

        data
      end
    end
  end
end
