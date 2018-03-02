FROM ruby:2.3

RUN mkdir -p /bundle-cache

COPY ./ruby/Gemfile /bundle-cache
COPY ./ruby/Gemfile.lock /bundle-cache
COPY ./bin /bin

RUN DEBIAN_FRONTEND=noninteractive apt-get update -qq && \
    DEBIAN_FRONTEND=noninteractive apt-get install -qq -y rubygems bundler cmake build-essential libicu-dev && \
    cd /bundle-cache && bundle install --path vendor/bundle
