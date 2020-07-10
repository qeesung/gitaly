module Praefect
  class Transaction
    PRAEFECT_SERVER_KEY = "praefect-server".freeze
    PRAEFECT_SERVER_ENV = "PRAEFECT_SERVER".freeze
    TRANSACTION_KEY = "transaction".freeze
    TRANSACTION_ENV = "REFERENCE_TRANSACTION".freeze

    def self.from_metadata(metadata)
      new(metadata[PRAEFECT_SERVER_KEY], metadata[TRANSACTION_KEY])
    end

    def initialize(server, transaction)
      @server = server
      @transaction = transaction
    end

    def env_vars
      {
        TRANSACTION_ENV => @transaction,
        PRAEFECT_SERVER_ENV => @server
      }.reject { |_, v| v.nil? }
    end
  end
end
