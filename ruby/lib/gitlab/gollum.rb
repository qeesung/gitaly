module Gollum
  GIT_ADAPTER = "rugged".freeze
end
require "gollum-lib"

Gollum::Page.per_page = 20 # Magic number from Kaminari.config.default_per_page

module Gollum
  class Page
    def text_data(encoding=nil)
      data = if raw_data.respond_to?(:encoding)
               raw_data.force_encoding(encoding || Encoding::UTF_8)
             else
               raw_data
             end

      Gitlab::EncodingHelper.encode!(data)
    end
  end
end
