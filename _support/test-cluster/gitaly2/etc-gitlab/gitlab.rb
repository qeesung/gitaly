gitaly['enable'] = true
gitaly['listen_addr'] = ':6666'

# This instance will be serving the 'gitaly2' repository storage
git_data_dirs({
  'gitaly2' => '/var/opt/gitlab/git-data-2'
})

# Disable as many Omnibus services as we can
unicorn['enable'] = false
sidekiq['enable'] = false
gitlab_workhorse['enable'] = false
gitlab_monitor['enable'] = false
prometheus_monitoring['enable'] = false
redis['enable'] = false
postgresql['enable']=false
nginx['enable'] = false

# We need these settings to prevent Omnibus from erroring out because
# Postgres/Redis are unavailable
gitlab_rails['rake_cache_clear'] = false
gitlab_rails['auto_migrate'] = false

gitlab_rails['redis_host'] = 'app1'
gitlab_rails['redis_port'] = 6379

# Use the remote internal API
gitlab_rails['internal_api_url'] = 'http://app1:80'

# We have to use the same token in all hosts for internal API authentication
gitlab_shell['secret_token'] = 'd07589078736b802db1b8aae2e61d8ba694c15837a7a015909c34b3f456417c231fffdb21352052620f9539c77a4ffb5d7c7050eb14d5f336585e9796df7bc1e'
