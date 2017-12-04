module Gollum
  GIT_ADAPTER = "rugged".freeze
end
require "gollum-lib"

module Gollum
  class Committer
    # Patch for UTF-8 path
    def method_missing(name, *args) # rubocop:disable Style/MethodMissing
      index.send(name, *args) # rubocop:disable GitlabSecurity/PublicSend
    end
  end

  class Wiki
    def pages(treeish=nil, limit: nil)
      tree_list((treeish || @ref), limit: limit)
    end

    def tree_list(ref, limit: nil)
      if (sha = @access.ref_to_sha(ref))
        commit = @access.commit(sha)
        tree_map_for(sha).each_with_object([]) do |entry, list|
          next list unless @page_class.valid_page_name?(entry.name)

          list << entry.page(self, commit)
          break list if limit && list.size >= limit

          list
        end
      else
        []
      end
    end
  end
end

Gollum::Page.per_page = 20 # Magic number from Kaminari.config.default_per_page
