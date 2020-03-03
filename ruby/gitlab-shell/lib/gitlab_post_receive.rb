require_relative 'gitlab_init'
require_relative 'gitlab_net'
require_relative 'gitlab_metrics'
require_relative 'message'
require 'json'
require 'base64'
require 'securerandom'

class GitlabPostReceive
  attr_reader :config, :gl_repository, :repo_path, :changes, :jid, :output_stream

  def initialize(gl_repository, repo_path, gl_id, changes, push_options, output_stream = $stdout)
    @config = GitlabConfig.new
    @gl_repository = gl_repository
    @repo_path = repo_path.strip
    @gl_id = gl_id
    @changes = changes
    @push_options = push_options
    @jid = SecureRandom.hex(12)
    @output_stream = output_stream
  end

  def exec
    response = GitlabMetrics.measure("post-receive") do
      api.post_receive(gl_repository, @gl_id, changes, @push_options)
    end

    return false unless response

    print_messages(response['messages'])

    response['reference_counter_decreased']
  rescue GitlabNet::ApiUnreachableError
    false
  end

  protected

  def api
    @api ||= GitlabNet.new
  end

  def print_alert(message)
    Message.new('alert', message).print(output_stream)
  end

  def print_messages(response_messages)
    return if response_messages.nil? || response_messages.none?

    puts

    response_messages.each do |response_message|
      Message.new(response_message['type'], response_message['message']).print(output_stream)

      puts
    end
  end

  private

  def puts(*args)
    output_stream.puts(*args)
  end
end
