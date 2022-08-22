require 'licensee'

module GitalyServer
  class RepositoryService < Gitaly::RepositoryService::Service
    include Utils

    def find_license(request, call)
      repo = Gitlab::Git::Repository.from_gitaly(request.repository, call)

      begin
        project = ::Licensee.project(repo.path)
        return Gitaly::FindLicenseResponse.new(license_short_name: "") unless project&.license

        license = project.license
        return Gitaly::FindLicenseResponse.new(
          license_short_name: license.key || "",
          license_name: license.name || "",
          license_url: license.url || "",
          license_path: project.matched_file&.filename,
          license_nickname: license.nickname || ""
        )
      rescue Rugged::Error
      end

      Gitaly::FindLicenseResponse.new(license_short_name: "")
    end
  end
end
