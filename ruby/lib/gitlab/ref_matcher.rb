# frozen_string_literal: true

module GitLab
  class RefMatcher
    def initialize(ref_name_or_pattern)
      @ref_name_or_pattern = ref_name_or_pattern
    end

    # Checks if the protected ref matches the given ref name.
    def matches?(ref_name)
      return false if @ref_name_or_pattern.blank?

      exact_match?(ref_name) || wildcard_match?(ref_name)
    end

    private

    # Checks if this protected ref contains a wildcard
    def wildcard?
      @ref_name_or_pattern&.include?('*')
    end

    def exact_match?(ref_name)
      @ref_name_or_pattern == ref_name
    end

    def wildcard_match?(ref_name)
      return false unless wildcard?

      ref_name.match(wildcard_regex).present?
    end

    def wildcard_regex
      @wildcard_regex ||= begin
                            split = @ref_name_or_pattern.split('*', -1) # Use -1 to correctly handle trailing '*'
                            quoted_segments = split.map { |segment| Regexp.quote(segment) }
                            quoted_segments.join('.*?')
                          end
    end
  end
end
