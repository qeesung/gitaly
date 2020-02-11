# frozen_string_literal: true

module GitalyServer
  # Interface to Ruby-specific feature flags passed to the Gitaly Ruby server
  # via headers.
  class FeatureFlags
    # Only headers prefixed with this String will be made available
    HEADER_PREFIX = 'gitaly-feature-ruby-'

    def initialize(metadata)
      @flags = metadata.select do |key, _|
        key.start_with?(HEADER_PREFIX)
      end
    end

    # Check if a given flag is enabled
    #
    # The `gitaly-feature-ruby-` prefix is optional, and underscores are
    # translated to hyphens automatically.
    #
    # Examples
    #
    #   enabled?('gitaly-feature-ruby-my-flag')
    #   => true
    #
    #   enabled?(:my_flag)
    #   => true
    #
    #   enabled?('my-flag')
    #   => true
    #
    #   enabled?(:unknown_flag)
    #   => false
    def enabled?(flag)
      flag = normalize_flag(flag)

      @flags.fetch(flag, false) == 'true'
    end

    def disabled?(flag)
      !enabled?(flag)
    end

    def inspect
      pairs = @flags.map { |name, value| "#{name}=#{value}" }
      pairs.unshift(self.class.name)

      "#<#{pairs.join(' ')}>"
    end

    private

    def normalize_flag(flag)
      flag = flag.to_s.delete_prefix(HEADER_PREFIX).tr('_', '-')

      "#{HEADER_PREFIX}#{flag}"
    end
  end
end
