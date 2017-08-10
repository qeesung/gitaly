FROM registry.gitlab.com/gitlab-org/gitlab-build-images:ruby-2.3-golang-1.8-git-2.13

RUN mkdir -p /bundle-cache

COPY Gemfile /bundle-cache
COPY Gemfile.lock /bundle-cache

RUN apt-get update -qq && \
    apt-get install -qq -y rubygems bundler cmake build-essential libicu-dev && \
    cd /bundle-cache && bundle install --path vendor/bundle
