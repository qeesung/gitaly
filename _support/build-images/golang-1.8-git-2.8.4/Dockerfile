FROM registry.gitlab.com/gitlab-org/gitlab-build-images:golang-1.8-git-2.8.4

RUN mkdir -p /bundle-cache

COPY Gemfile /bundle-cache
COPY Gemfile.lock /bundle-cache

RUN apt-get update -qq && \
    apt-get install -qq -y rubygems bundler cmake build-essential libicu-dev && \
    cd /bundle-cache && bundle install
