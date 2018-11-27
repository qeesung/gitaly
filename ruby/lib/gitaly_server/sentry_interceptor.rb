require 'grpc'
require 'raven/base'

require_relative 'sentry.rb'
module GitalyServer
  class SentryInterceptor < GRPC::ServerInterceptor
    # Intercept a unary request response call
    def request_response(request: nil, call: nil, method: nil)
      start = Time.now
      yield
    rescue => e
      time_ms = Time.now - start
      handle_exception(e, call, method, time_ms)
    end

    # Intercept a server streaming call
    def server_streamer(request: nil, call: nil, method: nil)
      start = Time.now
      yield
    rescue => e
      time_ms = Time.now - start
      handle_exception(e, call, method, time_ms)
    end

    # Intercept a client streaming call
    def client_streamer(call: nil, method: nil)
      start = Time.now
      yield
    rescue => e
      time_ms = Time.now - start
      handle_exception(e, call, method, time_ms)
    end

    # Intercept a BiDi streaming call
    def bidi_streamer(requests: nil, call: nil, method: nil)
      start = Time.now
      yield
    rescue => e
      time_ms = Time.now - start
      handle_exception(e, call, method, time_ms)
    end

    private

    def handle_exception(exc, call, method, time_ms)
      raise exc unless GitalyServer::Sentry.enabled?

      grpc_method = "#{method.owner.name}##{method.name}"
      tags = {
        'system' => 'gitaly-ruby',
        'gitaly-ruby.method' => grpc_method,
        'gitaly-ruby.time_ms' => format("%.0f", (time_ms * 1000))
      }
      tags.merge!(call.metadata)

      Raven.tags_context(tags)
      Raven.capture_exception(exc, fingerprint: ['gitaly-ruby', grpc_method, exc.message])

      raise exc
    end
  end
end
