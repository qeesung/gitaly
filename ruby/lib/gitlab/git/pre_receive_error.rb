module Gitlab
  module Git
    class PreReceiveError
      # In gitlab-rails this method applies HTML sanitization.
      def nlbr(str)
        str
      end
    end
  end
end
