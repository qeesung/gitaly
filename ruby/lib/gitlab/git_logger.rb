require 'logger'

module Gitlab
  GitLogger = Logger.new(STDOUT)
  GitLogger.progname = 'githost.log'
  GitLogger.level = 'info'
end
