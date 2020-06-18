# Makefile for Gitaly

SHELL = /usr/bin/env bash -eo pipefail

# Host information
OS   := $(shell uname)
ARCH := $(shell uname -m)

# Directories
SOURCE_DIR       := $(abspath $(dir $(lastword ${MAKEFILE_LIST})))
BUILD_DIR        := ${SOURCE_DIR}/_build
COVERAGE_DIR     := ${BUILD_DIR}/cover
GITALY_RUBY_DIR  := ${SOURCE_DIR}/ruby
GITLAB_SHELL_DIR := ${GITALY_RUBY_DIR}/gitlab-shell

# These variables may be overridden at runtime by top-level make
PREFIX           ?= /usr/local
prefix           ?= ${PREFIX}
exec_prefix      ?= ${prefix}
bindir           ?= ${exec_prefix}/bin
INSTALL_DEST_DIR := ${DESTDIR}${bindir}
ASSEMBLY_ROOT    ?= ${BUILD_DIR}/assembly
GIT_PREFIX       ?= ${GIT_INSTALL_DIR}

# Tools
GOIMPORTS         := ${BUILD_DIR}/bin/goimports
GITALYFMT         := ${BUILD_DIR}/bin/gitalyfmt
GOLANGCI_LINT     := ${BUILD_DIR}/bin/golangci-lint
GO_LICENSES       := ${BUILD_DIR}/bin/go-licenses
PROTOC            := ${BUILD_DIR}/protoc/bin/protoc
PROTOC_GEN_GO     := ${BUILD_DIR}/bin/protoc-gen-go
PROTOC_GEN_GITALY := ${BUILD_DIR}/bin/protoc-gen-gitaly
GO_JUNIT_REPORT   := ${BUILD_DIR}/bin/go-junit-report

# Build information
BUNDLE_FLAGS    ?= $(shell test -f ${SOURCE_DIR}/../.gdk-install-root && echo --no-deployment || echo --deployment)
GITALY_PACKAGE  := gitlab.com/gitlab-org/gitaly
BUILD_TIME      := $(shell date +"%Y%m%d.%H%M%S")
GITALY_VERSION  := $(shell git describe --match v* 2>/dev/null | sed 's/^v//' || cat ${SOURCE_DIR}/VERSION 2>/dev/null || echo unknown)
GO_LDFLAGS      := -ldflags '-X ${GITALY_PACKAGE}/internal/version.version=${GITALY_VERSION} -X ${GITALY_PACKAGE}/internal/version.buildtime=${BUILD_TIME}'
GO_TEST_LDFLAGS := -X gitlab.com/gitlab-org/gitaly/auth.timestampThreshold=5s
GO_BUILD_TAGS   := tracer_static tracer_static_jaeger continuous_profiler_stackdriver

# Dependency versions
GOLANGCI_LINT_VERSION ?= 1.24.0
PROTOC_VERSION        ?= 3.6.1

# Dependency downloads
ifeq (${OS},Darwin)
PROTOC_URL            := https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-osx-x86_64.zip
PROTOC_HASH           := 0decc6ce5beed07f8c20361ddeb5ac7666f09cf34572cca530e16814093f9c0c
GOLANGCI_LINT_ARCHIVE := golangci-lint-${GOLANGCI_LINT_VERSION}-darwin-amd64
GOLANGCI_LINT_HASH    := f05af56f15ebbcf77663a8955d1e39009b584ce8ea4c5583669369d80353a113
else ifeq (${OS},Linux)
PROTOC_URL            := https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip
PROTOC_HASH           := 6003de742ea3fcf703cfec1cd4a3380fd143081a2eb0e559065563496af27807
GOLANGCI_LINT_ARCHIVE := golangci-lint-${GOLANGCI_LINT_VERSION}-linux-amd64
GOLANGCI_LINT_HASH    := 241ca454102e909de04957ff8a5754c757cefa255758b3e1fba8a4533d19d179
else
$(error Unsupported OS: ${OS})
endif
GOLANGCI_LINT_URL := https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/${GOLANGCI_LINT_ARCHIVE}.tar.gz

# Git target
GIT_VERSION      ?= v2.27.0
GIT_REPO_URL     ?= https://gitlab.com/gitlab-org/gitlab-git.git
GIT_BINARIES_URL ?= https://gitlab.com/gitlab-org/gitlab-git/-/jobs/artifacts/${GIT_VERSION}/raw/git_full_bins.tgz?job=build
GIT_INSTALL_DIR  := ${BUILD_DIR}/git
GIT_SOURCE_DIR   := ${BUILD_DIR}/src/git

ifeq (${GIT_BUILD_OPTIONS},)
# activate developer checks
GIT_BUILD_OPTIONS += DEVELOPER=1
# make it easy to debug in case of crashes
GIT_BUILD_OPTIONS += CFLAGS='-O0 -g3'
GIT_BUILD_OPTIONS += NO_PERL=YesPlease
GIT_BUILD_OPTIONS += NO_EXPAT=YesPlease
GIT_BUILD_OPTIONS += NO_TCLTK=YesPlease
# fix compilation on musl libc
GIT_BUILD_OPTIONS += NO_REGEX=YesPlease
GIT_BUILD_OPTIONS += NO_GETTEXT=YesPlease
GIT_BUILD_OPTIONS += NO_PYTHON=YesPlease
GIT_BUILD_OPTIONS += NO_INSTALL_HARDLINKS=YesPlease
GIT_BUILD_OPTIONS += NO_R_TO_GCC_LINKER=YesPlease
endif

# These variables control test options and artifacts
TEST_OPTIONS    ?=
TEST_REPORT_DIR ?= ${BUILD_DIR}/reports
TEST_OUTPUT     ?= ${TEST_REPORT_DIR}/go-tests-output-${CI_JOB_NAME}.txt
TEST_REPORT     ?= ${TEST_REPORT_DIR}/go-tests-report-${CI_JOB_NAME}.xml
TEST_EXIT       ?= ${TEST_REPORT_DIR}/go-tests-exit-${CI_JOB_NAME}.txt
TEST_REPO_DIR   := ${SOURCE_DIR}/internal/testhelper/testdata/data
TEST_REPO       := ${TEST_REPO_DIR}/gitlab-test.git
TEST_REPO_GIT   := ${TEST_REPO_DIR}/gitlab-git-test.git

# Find all commands.
find_commands         = $(notdir $(shell find ${SOURCE_DIR}/cmd -mindepth 1 -maxdepth 1 -type d -print))
# Find all command binaries.
find_command_binaries = $(addprefix ${BUILD_DIR}/bin/, $(call find_commands))

# Find all Go source files.
find_go_sources  = $(shell find ${SOURCE_DIR} -type d \( -name ruby -o -name vendor -o -name testdata -o -name '_*' -o -path '*/proto/go' \) -prune -o -type f -name '*.go' -not -name '*.pb.go' -print | sort -u)
# Find all Go packages.
find_go_packages = $(dir $(call find_go_sources, 's|[^/]*\.go||'))

unexport GOROOT
export GOBIN        = ${BUILD_DIR}/bin
export GO111MODULE  = on
export GOPROXY     ?= https://proxy.golang.org
export PATH        := ${BUILD_DIR}/bin:${PATH}

.NOTPARALLEL:

.PHONY: all
all: INSTALL_DEST_DIR = ${SOURCE_DIR}
all: install

.PHONY: build
build: ${SOURCE_DIR}/.ruby-bundle
	go install ${GO_LDFLAGS} -tags "${GO_BUILD_TAGS}" $(addprefix ${GITALY_PACKAGE}/cmd/, $(call find_commands))

.PHONY: install
install: build
	mkdir -p ${INSTALL_DEST_DIR}
	install $(call find_command_binaries) ${INSTALL_DEST_DIR}

.PHONY: force-ruby-bundle
force-ruby-bundle:
	rm -f ${SOURCE_DIR}/.ruby-bundle

# Assembles all runtime components into a directory
# Used by the GDK: run 'make assemble ASSEMBLY_ROOT=.../gitaly'
.PHONY: assemble
assemble: force-ruby-bundle assemble-internal

# assemble-internal does not force 'bundle install' to run again
.PHONY: assemble-internal
assemble-internal: assemble-ruby assemble-go

.PHONY: assemble-go
assemble-go: build
	rm -rf ${ASSEMBLY_ROOT}/bin
	mkdir -p ${ASSEMBLY_ROOT}/bin
	install $(call find_command_binaries) ${ASSEMBLY_ROOT}/bin

.PHONY: assemble-ruby
assemble-ruby:
	mkdir -p ${ASSEMBLY_ROOT}
	rm -rf ${GITALY_RUBY_DIR}/tmp ${GITLAB_SHELL_DIR}/tmp
	mkdir -p ${ASSEMBLY_ROOT}/ruby/
	rsync -a --delete  ${GITALY_RUBY_DIR}/ ${ASSEMBLY_ROOT}/ruby/
	rm -rf ${ASSEMBLY_ROOT}/ruby/spec ${ASSEMBLY_ROOT}/ruby/gitlab-shell/spec ${ASSEMBLY_ROOT}/ruby/gitlab-shell/gitlab-shell.log

.PHONY: binaries
binaries: assemble
	@if [ ${ARCH} != 'x86_64' ]; then echo Incorrect architecture for build: ${ARCH}; exit 1; fi
	@cd ${ASSEMBLY_ROOT} && shasum -a 256 bin/* | tee checksums.sha256.txt

.PHONY: prepare-tests
prepare-tests: ${GITLAB_SHELL_DIR}/config.yml ${TEST_REPO} ${TEST_REPO_GIT} ${SOURCE_DIR}/.ruby-bundle

.PHONY: test
test: test-go rspec rspec-gitlab-shell

.PHONY: test-go
test-go: prepare-tests ${GO_JUNIT_REPORT}
	@mkdir -p ${TEST_REPORT_DIR}
	@echo 0 >${TEST_EXIT} && \
		(go test ${TEST_OPTIONS} -v -tags "${GO_BUILD_TAGS}" -ldflags='${GO_TEST_LDFLAGS}' -count=1 $(call find_go_packages) 2>&1 | tee ${TEST_OUTPUT} || echo $$? >${TEST_EXIT}) && \
		${GO_JUNIT_REPORT} <${TEST_OUTPUT} >${TEST_REPORT} && \
		exit `cat ${TEST_EXIT}`

.PHONY: test-with-proxies
test-with-proxies: prepare-tests
	@go test -tags "${GO_BUILD_TAGS}" -count=1  -exec ${SOURCE_DIR}/_support/bad-proxies ${GITALY_PACKAGE}/internal/rubyserver/

.PHONY: test-with-praefect
test-with-praefect: build prepare-tests
	@GITALY_TEST_PRAEFECT_BIN=${BUILD_DIR}/bin/praefect go test -tags "${GO_BUILD_TAGS}" -ldflags='${GO_TEST_LDFLAGS}' -count=1 $(call find_go_packages) # count=1 bypasses go 1.10 test caching

.PHONY: race-go
race-go: TEST_OPTIONS = -race
race-go: test-go

.PHONY: rspec
rspec: assemble-go prepare-tests
	cd ${GITALY_RUBY_DIR} && bundle exec rspec

.PHONY: rspec-gitlab-shell
rspec-gitlab-shell: ${GITLAB_SHELL_DIR}/config.yml assemble-go prepare-tests
	@cd ${GITALY_RUBY_DIR} && bundle exec bin/ruby-cd ${GITLAB_SHELL_DIR} rspec

.PHONY: test-postgres
test-postgres: prepare-tests
	@go test -tags postgres -count=1 gitlab.com/gitlab-org/gitaly/internal/praefect/...

.PHONY: verify
verify: check-mod-tidy check-formatting notice-up-to-date check-proto rubocop

.PHONY: check-mod-tidy
check-mod-tidy:
	@${SOURCE_DIR}/_support/check-mod-tidy

.PHONY: lint
lint: ${GOLANGCI_LINT}
	@${GOLANGCI_LINT} cache clean && ${GOLANGCI_LINT} run --out-format tab --config ${SOURCE_DIR}/.golangci.yml

.PHONY: check-formatting
check-formatting: ${GITALYFMT}
	@${GITALYFMT} $(call find_go_sources) | awk '{ print } END { if(NR>0) { print "Formatting error, run make format"; exit(1) } }'

.PHONY: format
format: ${GOIMPORTS} ${GITALYFMT}
	@${GOIMPORTS} -w -l $(call find_go_sources)
	@${GITALYFMT} -w $(call find_go_sources)
	@${GOIMPORTS} -w -l $(call find_go_sources)

.PHONY: staticcheck-deprecations
staticcheck-deprecations: ${GOLANGCI_LINT}
	@${GOLANGCI_LINT} run --out-format tab --config ${SOURCE_DIR}/_support/golangci.warnings.yml

.PHONY: lint-warnings
lint-warnings: staticcheck-deprecations
	# Runs verification analysis that is okay to fail (but not ignore completely)

.PHONY: notice-up-to-date
notice-up-to-date: notice-tmp
	# notice-up-to-date
	@(cmp ${BUILD_DIR}/NOTICE ${SOURCE_DIR}/NOTICE) || (echo >&2 "NOTICE requires update: 'make notice'" && false)

.PHONY: notice
notice: notice-tmp
	mv ${BUILD_DIR}/NOTICE ${SOURCE_DIR}/NOTICE

.PHONY: notice-tmp
notice-tmp: ${GO_LICENSES} clean-ruby-vendor-go
	rm -rf ${BUILD_DIR}/licenses
	${GO_LICENSES} save ./... --save_path=${BUILD_DIR}/licenses
	go run ${SOURCE_DIR}/_support/noticegen/noticegen.go -source ${BUILD_DIR}/licenses -template ${SOURCE_DIR}/_support/noticegen/notice.template > ${BUILD_DIR}/NOTICE

.PHONY: clean
clean:
	rm -rf ${BUILD_DIR} ${SOURCE_DIR}/internal/testhelper/testdata/data/ ${SOURCE_DIR}/ruby/.bundle/ ${SOURCE_DIR}/ruby/gitlab-shell/config.yml ${SOURCE_DIR}/ruby/vendor/bundle/ $(addprefix ${SOURCE_DIR}/, $(notdir $(call find_commands)))

.PHONY: clean-ruby-vendor-go
clean-ruby-vendor-go:
	mkdir -p ${SOURCE_DIR}/ruby/vendor && find ${SOURCE_DIR}/ruby/vendor -type f -name '*.go' -delete

.PHONY: check-proto
check-proto: proto no-changes

.PHONY: rubocop
rubocop: ${SOURCE_DIR}/.ruby-bundle
	cd  ${GITALY_RUBY_DIR} && bundle exec rubocop --parallel

.PHONY: cover
cover: prepare-tests
	@echo "NOTE: make cover does not exit 1 on failure, don't use it to check for tests success!"
	mkdir -p "${COVERAGE_DIR}"
	rm -f "${COVERAGE_DIR}/all.merged" "${COVERAGE_DIR}/all.html"
	@go test -ldflags='${GO_TEST_LDFLAGS}' -coverprofile "${COVERAGE_DIR}/all.merged" $(call find_go_packages)
	@go tool cover -html  "${COVERAGE_DIR}/all.merged" -o "${COVERAGE_DIR}/all.html"
	@echo ""
	@echo "=====> Total test coverage: <====="
	@echo ""
	@go tool cover -func "${COVERAGE_DIR}/all.merged"

.PHONY: docker
docker:
	rm -rf ${BUILD_DIR}/docker/
	mkdir -p ${BUILD_DIR}/docker/bin/
	rm -rf  ${GITALY_RUBY_DIR}/tmp
	cp -r  ${GITALY_RUBY_DIR} ${BUILD_DIR}/docker/ruby
	rm -rf ${BUILD_DIR}/docker/ruby/vendor/bundle
	for command in $(call find_commands); do \
		GOOS=linux GOARCH=amd64 go build -tags "${GO_BUILD_TAGS}" ${GO_LDFLAGS} -o "${BUILD_DIR}/docker/bin/$${command}" ${GITALY_PACKAGE}/cmd/$${command}; \
	done
	cp ${SOURCE_DIR}/Dockerfile ${BUILD_DIR}/docker/
	docker build -t gitlab/gitaly:v${GITALY_VERSION} -t gitlab/gitaly:latest ${BUILD_DIR}/docker/

.PHONY: proto
proto: ${PROTOC_GEN_GITALY} ${SOURCE_DIR}/.ruby-bundle
	${PROTOC} --gitaly_out=proto_dir=./proto,gitalypb_dir=./proto/go/gitalypb:. --go_out=paths=source_relative,plugins=grpc:./proto/go/gitalypb -I./proto ./proto/*.proto
	${SOURCE_DIR}/_support/generate-proto-ruby
	# this part is related to the generation of sources from testing proto files
	${PROTOC} --plugin=${PROTOC_GEN_GO} --go_out=plugins=grpc:. internal/praefect/grpc-proxy/testdata/test.proto

.PHONY: proto-lint
proto-lint: ${PROTOC} ${PROTOC_GEN_GO}
	mkdir -p ${SOURCE_DIR}/proto/go/gitalypb
	rm -rf ${SOURCE_DIR}/proto/go/gitalypb/*.pb.go
	${PROTOC} --go_out=paths=source_relative:./proto/go/gitalypb -I./proto ./proto/lint.proto

.PHONY: no-changes
no-changes:
	@git status --porcelain | awk '{ print } END { if (NR > 0) { exit 1 } }'

.PHONY: smoke-test
smoke-test: all rspec
	@go test ./internal/rubyserver

.PHONY: download-git
download-git: | ${BUILD_DIR}
	@echo "Getting Git from ${GIT_BINARIES_URL}"
	curl -o ${BUILD_DIR}/git_full_bins.tgz --silent --show-error -L ${GIT_BINARIES_URL}
	rm -rf ${GIT_INSTALL_DIR}
	mkdir -p ${GIT_INSTALL_DIR}
	tar -C ${GIT_INSTALL_DIR} -xvzf ${BUILD_DIR}/git_full_bins.tgz

.PHONY: build-git
build-git:
	@echo "Getting Git from ${GIT_REPO_URL}"
	rm -rf ${GIT_SOURCE_DIR} ${GIT_INSTALL_DIR}
	git clone ${GIT_REPO_URL} ${GIT_SOURCE_DIR}
	git -C ${GIT_SOURCE_DIR} checkout ${GIT_VERSION}
	rm -rf ${GIT_INSTALL_DIR}
	mkdir -p ${GIT_INSTALL_DIR}
	${MAKE} -C ${GIT_SOURCE_DIR} -j$(shell nproc) prefix=${GIT_PREFIX} ${GIT_BUILD_OPTIONS} install

# This file is used by Omnibus and CNG to skip the "bundle install"
# step. Both Omnibus and CNG assume it is in the Gitaly root, not in
# _build. Hence the '../' in front.
${SOURCE_DIR}/.ruby-bundle: ${GITALY_RUBY_DIR}/Gemfile.lock ${GITALY_RUBY_DIR}/Gemfile
	cd ${GITALY_RUBY_DIR} && bundle config # for debugging
	cd ${GITALY_RUBY_DIR} && bundle install ${BUNDLE_FLAGS}
	touch $@

${BUILD_DIR}:
	mkdir -p ${BUILD_DIR}

${BUILD_DIR}/bin: | ${BUILD_DIR}
	mkdir -p ${BUILD_DIR}/bin

${BUILD_DIR}/go.mod: | ${BUILD_DIR}
	cd ${BUILD_DIR} && go mod init _build

${PROTOC}: ${BUILD_DIR}/protoc.zip | ${BUILD_DIR}
	rm -rf ${BUILD_DIR}/protoc
	mkdir -p ${BUILD_DIR}/protoc
	cd ${BUILD_DIR}/protoc && unzip ${BUILD_DIR}/protoc.zip

${BUILD_DIR}/protoc.zip: | ${BUILD_DIR}
	curl -o $@.tmp --silent --show-error -L ${PROTOC_URL}
	printf '${PROTOC_HASH}  $@.tmp' | shasum -a256 -c -
	mv $@.tmp $@

${GOIMPORTS}: ${BUILD_DIR}/go.mod | ${BUILD_DIR}/bin
	cd ${BUILD_DIR} && go get golang.org/x/tools/cmd/goimports@2538eef75904eff384a2551359968e40c207d9d2

${GO_JUNIT_REPORT}: ${BUILD_DIR}/go.mod | ${BUILD_DIR}/bin
	cd ${BUILD_DIR} && go get github.com/jstemmer/go-junit-report@984a47ca6b0a7d704c4b589852051b4d7865aa17

${GITALYFMT}: ${BUILD_DIR}/bin
	go build -o $@ ${SOURCE_DIR}/internal/cmd/gitalyfmt

${GO_LICENSES}: ${BUILD_DIR}/go.mod | ${BUILD_DIR}/bin
	cd ${BUILD_DIR} && go get github.com/google/go-licenses@0fa8c766a59182ce9fd94169ddb52abe568b7f4e

${PROTOC_GEN_GO}: ${BUILD_DIR}/go.mod | ${BUILD_DIR}/bin
	cd ${BUILD_DIR} && go get github.com/golang/protobuf/protoc-gen-go@v1.3.2

${PROTOC_GEN_GITALY}: ${BUILD_DIR}/go.mod proto-lint | ${BUILD_DIR}/bin
	go build -o $@ gitlab.com/gitlab-org/gitaly/proto/go/internal/cmd/protoc-gen-gitaly

${GOLANGCI_LINT}: ${BUILD_DIR}/golangci-lint.tar.gz | ${BUILD_DIR}/bin
	tar -x -z --strip-components 1 -C ${BUILD_DIR}/bin -f ${BUILD_DIR}/golangci-lint.tar.gz ${GOLANGCI_LINT_ARCHIVE}/golangci-lint
	touch $@

${BUILD_DIR}/golangci-lint.tar.gz: | ${BUILD_DIR}
	curl -o $@.tmp --silent --show-error -L ${GOLANGCI_LINT_URL}
	printf '${GOLANGCI_LINT_HASH}  $@.tmp' | shasum -a256 -c -
	mv $@.tmp $@

${TEST_REPO}:
	git clone --bare --quiet https://gitlab.com/gitlab-org/gitlab-test.git $@
	# Git notes aren't fetched by default with git clone
	git -C $@ fetch origin refs/notes/*:refs/notes/*
	rm -rf $@/refs
	mkdir -p $@/refs/heads $@/refs/tags
	cp ${SOURCE_DIR}/_support/gitlab-test.git-packed-refs $@/packed-refs
	git -C $@ fsck --no-progress

${TEST_REPO_GIT}:
	git clone --bare --quiet https://gitlab.com/gitlab-org/gitlab-git-test.git $@
	rm -rf $@/refs
	mkdir -p $@/refs/heads $@/refs/tags
	cp ${SOURCE_DIR}/_support/gitlab-git-test.git-packed-refs $@/packed-refs
	git -C $@ fsck --no-progress

${GITLAB_SHELL_DIR}/config.yml: ${GITLAB_SHELL_DIR}/config.yml.example
	cp $< $@
