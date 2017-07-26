require 'linguist'
require 'rugged'

module GitalyServer
  class CommitService < Gitaly::CommitService::Service
    def commit_languages(request, _call)
      rugged_repo = Rugged::Repository.new(GitalyServer.repo_path(_call))
      revision = request.revision
      revision = rugged_repo.head.target_id if revision.empty?

      languages = Linguist::Repository.new(rugged_repo, revision).languages

      total = languages.values.inject(0, :+)
      language_messages = languages.map do |name, share|
        Gitaly::CommitLanguagesResponse::Language.new(
          name: name,
          share: (share.to_f * 100 / total).round(2),
          color: Linguist::Language[name].color || "##{Digest::SHA256.hexdigest(name)[0...6]}"
        ) 
      end
      language_messages.sort! do |x, y|
        y.share <=> x.share
      end

      Gitaly::CommitLanguagesResponse.new(languages: language_messages)
    end
  end
end
