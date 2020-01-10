module Gitlab
  module Git
    class HooksService
      attr_accessor :oldrev, :newrev, :ref

      def execute(pusher, repository, oldrev, newrev, ref, skip_ci: false)
        @repository  = repository
        @gl_id       = pusher.gl_id
        @gl_username = pusher.username
        @oldrev      = oldrev
        @newrev      = newrev
        @ref         = ref
        @skip_ci     = skip_ci

        %w(pre-receive update).each do |hook_name|
          status, message = run_hook(hook_name)

          raise PreReceiveError, message unless status
        end

        yield(self).tap do
          run_hook('post-receive', skip_ci: skip_ci)
        end
      end

      private

      def run_hook(name, skip_ci: false)
        hook = Gitlab::Git::Hook.new(name, @repository)
        hook.trigger(@gl_id, @gl_username, oldrev, newrev, ref, skip_ci: skip_ci)
      end
    end
  end
end
