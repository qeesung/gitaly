#!/usr/bin/env ruby

require 'net/http'
require 'uri'
require 'json'

def gitlab_api(url, body)
  uri = URI.parse(url)

  header = {
    'Content-Type': 'application/json',
    'PRIVATE-TOKEN': ENV['PRIVATE_TOKEN']
  }

  # Create the HTTP objects
  Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == 'https') do |http|
    request = Net::HTTP::Post.new(uri.request_uri, header)
    request.body = body.to_json

    response = http.request(request)
    raise "Request to #{url} failed: #{response.body}" unless Integer(response.code) < 400
    response.body
  end
end

def update_tag(project_id, tag_version)
  commit = {
    "branch": "gitaly-version-#{tag_version}",
    "start_branch": "master",
    "commit_message": "Update Gitaly version to #{tag_version}",
    "actions": [{
      "action": "update",
      "file_path": "GITALY_SERVER_VERSION",
      "content": "#{tag_version}"
    }]
  }

  gitlab_api("https://gitlab.com/api/v4/projects/#{project_id}/repository/commits", commit)
end

def create_mr(project_id, tag_version, assignee_id)
  merge_request = {
    "source_branch": "gitaly-version-#{tag_version}",
    "target_branch": "master",
    "title": "Upgrade Gitaly to #{tag_version}",
    "assignee_id": assignee_id,
    "description": "Upgrade Gitaly to #{tag_version}",
    "labels": "Gitaly",
    "remove_source_branch": true,
    "squash": true
  }

  gitlab_api("https://gitlab.com/api/v4/projects/#{project_id}/merge_requests", merge_request)
end

project_id = ENV["GITLAB_CE_PROJECT_ID"]
tag_version = ENV["CI_COMMIT_TAG"]
assignee_id = ENV["GITLAB_USER_ID"]

update_tag(project_id, tag_version)
create_mr(project_id, tag_version, assignee_id)
