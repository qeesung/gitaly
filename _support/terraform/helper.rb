require 'json'

require_relative '../run.rb'

def terraform_initialized?
  File.exist?('.terraform')
end

def terraform_any_machines?
  state = JSON.parse(capture!(%w[terraform show -json]))
  state.has_key?('values')
end
