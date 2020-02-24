require 'base64'
require 'json'

module GitalyServer
  class Client
    ServerLookupError = Class.new(StandardError)

    def initialize(encoded_servers)
      @servers = encoded_servers.present? ? JSON.parse(Base64.strict_decode64(encoded_servers)) : {}
    end

    def shared_secret(storage)
      server(storage)['token']
    end

    def address(storage)
      server(storage)['address']
    end

    private

    def server(storage)
      raise ServerLookupError.new("cannot find gitaly address for storage #{storage.inspect}") unless @servers.has_key?(storage)

      @servers[storage]
    end
  end
end
