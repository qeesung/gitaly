# frozen_string_literal: true

require 'gitlab-dangerfiles'

Gitlab::Dangerfiles.for_project(self) do |gitlab_dangerfiles|
  gitlab_dangerfiles.config.files_to_category = {}.freeze

  Dir.each_child('danger/rules') do |rule|
    danger.import_dangerfile(path: "danger/rules/#{rule}")
  end

  gitlab_dangerfiles.import_plugins
  gitlab_dangerfiles.import_dangerfiles(except: %w[changes_size])
end
