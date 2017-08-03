module Gitlab
  module GitalyClient
    class << self
      def migrate(*args)
        yield false
      end
    end
  end
end
