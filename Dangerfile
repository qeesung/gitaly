CHANGELOG_FILE = "CHANGELOG.md"

def check_changelog
  unless git.modified_files.include?(CHANGELOG_FILE)
    warn("This MR is missing a CHANGELOG entry")
    return
  end

  patch = git.diff_for_file(CHANGELOG_FILE).patch
  unless patch.match?(/^\+- #{Regexp.quote(gitlab.mr_title)}\n/)
    fail('Changelog entry should match the MR title')
  end

  unless patch.match?(/^\+\s+#{Regexp.quote(gitlab.mr_json['web_url'])}\n/)
    fail('Changelog entry URL does not match the web url')
  end
end

check_changelog

fail("Please provide a MR description") if gitlab.mr_body.empty?

VENDOR_JSON = 'vendor/vendor.json'
fail("Expected #{VENDOR_JSON} to exist") unless File.exist?(VENDOR_JSON)

if git.modified_files.include?(VENDOR_JSON)
  require 'json'
  parsed_json = JSON.parse(File.read(VENDOR_JSON))

  proto = parsed_json["package"]&.find { |h| h["path"].start_with?("gitlab.com/gitlab-org/gitaly-proto") }

  unless proto["version"] && proto["version"] =~ /\Av\d+\./
    fail("gitaly-proto version is incorrect")
  end
end
