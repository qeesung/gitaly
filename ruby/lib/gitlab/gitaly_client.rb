module Gitlab
  module GitalyClient
    module MigrationStatus
      DISABLED = :fake_disabled
      OPT_IN = :fake_opt_in
      OPT_OUT = :fake_opt_out
    end

    class << self
      # In case we hit a method that tries to do a Gitaly RPC, we want to
      # prevent this most of the time.
      def migrate(*args)
        whitelist = [:fetch_ref]
        yield whitelist.include?(args.first)
      end
    end
  end
end
