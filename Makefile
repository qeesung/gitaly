# Makefile for Gitaly

# You can override options by creating a "config.mak" file in Gitaly's root
# directory.
-include config.mak

# Unexport environment variables which have an effect on Git itself.
# We need to keep GIT_PREFIX because it's used to determine where our
# self-built Git should be installed into. It's probably not going to
# matter much though.
unexport $(filter-out GIT_PREFIX,$(shell git rev-parse --local-env-vars))

# Call `make V=1` in order to print commands verbosely.
ifeq ($(V),1)
    Q =
else
    Q = @
endif

SHELL = /usr/bin/env bash -eo pipefail

# Host information
OS   := $(shell uname)
ARCH := $(shell uname -m)

# Directories
SOURCE_DIR       := $(abspath $(dir $(lastword ${MAKEFILE_LIST})))
BUILD_DIR        := ${SOURCE_DIR}/_build
DEPENDENCY_DIR   := ${BUILD_DIR}/deps
TOOLS_DIR        := ${BUILD_DIR}/tools
GITALY_RUBY_DIR  := ${SOURCE_DIR}/ruby
MODULE_VERSION   := $(notdir $(shell go list -m))

# These variables may be overridden at runtime by top-level make
## The prefix where Gitaly binaries will be installed to. Binaries will end up
## in ${PREFIX}/bin by default.
PREFIX           ?= /usr/local
prefix           ?= ${PREFIX}
exec_prefix      ?= ${prefix}
bindir           ?= ${exec_prefix}/bin
INSTALL_DEST_DIR := ${DESTDIR}${bindir}
## The prefix where Git will be installed to.
GIT_PREFIX       ?= ${GIT_DEFAULT_PREFIX}

# Tools
GIT                         := $(shell command -v git)
GOIMPORTS                   := ${TOOLS_DIR}/goimports
GOFUMPT                     := ${TOOLS_DIR}/gofumpt
GOLANGCI_LINT               := ${TOOLS_DIR}/golangci-lint
PROTOLINT                   := ${TOOLS_DIR}/protolint
GO_LICENSES                 := ${TOOLS_DIR}/go-licenses
PROTOC                      := ${TOOLS_DIR}/protoc
PROTOC_GEN_GO               := ${TOOLS_DIR}/protoc-gen-go
PROTOC_GEN_GO_GRPC          := ${TOOLS_DIR}/protoc-gen-go-grpc
PROTOC_GEN_GITALY_LINT      := ${TOOLS_DIR}/protoc-gen-gitaly-lint
PROTOC_GEN_GITALY_PROTOLIST := ${TOOLS_DIR}/protoc-gen-gitaly-protolist
GOTESTSUM                   := ${TOOLS_DIR}/gotestsum
GOCOVER_COBERTURA           := ${TOOLS_DIR}/gocover-cobertura
DELVE                       := ${TOOLS_DIR}/dlv

# Tool options
GOLANGCI_LINT_OPTIONS ?=
GOLANGCI_LINT_CONFIG  ?= ${SOURCE_DIR}/.golangci.yml

# Build information
GITALY_PACKAGE    := gitlab.com/gitlab-org/gitaly/v15
BUILD_TIME        := $(shell date +"%Y%m%d.%H%M%S")
GITALY_VERSION    := $(shell ${GIT} describe --match v* 2>/dev/null | sed 's/^v//' || cat ${SOURCE_DIR}/VERSION 2>/dev/null || echo unknown)
GO_LDFLAGS        := -X ${GITALY_PACKAGE}/internal/version.version=${GITALY_VERSION} -X ${GITALY_PACKAGE}/internal/version.buildtime=${BUILD_TIME} -X ${GITALY_PACKAGE}/internal/version.moduleVersion=${MODULE_VERSION}
SERVER_BUILD_TAGS := tracer_static,tracer_static_jaeger,tracer_static_stackdriver,continuous_profiler_stackdriver
GIT2GO_BUILD_TAGS := static,system_libgit2

# Temporary GNU build ID used as a placeholder value so that we can replace it
# with our own one after binaries have been built. This is the ASCII encoding
# of the string "TEMP_GITALY_BUILD_ID".
TEMPORARY_BUILD_ID := 54454D505F474954414C595F4255494C445F4944

## FIPS_MODE controls whether to build Gitaly and dependencies in FIPS mode.
## Set this to a non-empty value to enable it.
FIPS_MODE ?=

ifdef FIPS_MODE
    SERVER_BUILD_TAGS := ${SERVER_BUILD_TAGS},fips
    GIT2GO_BUILD_TAGS := ${GIT2GO_BUILD_TAGS},fips

    # Build Git with the OpenSSL backend for SHA256 in case FIPS-mode is
    # requested. Note that we explicitly don't do the same for SHA1: we
    # instead use SHA1DC to protect users against the SHAttered attack.
    GIT_FIPS_BUILD_OPTIONS := OPENSSL_SHA256=YesPlease

    export GITALY_TESTING_ENABLE_FIPS := YesPlease
endif

# Dependency versions
GOLANGCI_LINT_VERSION     ?= v1.46.2
PROTOLINT_VERSION         ?= v0.38.1
GOCOVER_COBERTURA_VERSION ?= aaee18c8195c3f2d90e5ef80ca918d265463842a
GOFUMPT_VERSION           ?= v0.3.1
GOIMPORTS_VERSION         ?= v0.1.10
GOTESTSUM_VERSION         ?= v1.8.1
GO_LICENSES_VERSION       ?= v1.2.1
# https://pkg.go.dev/github.com/protocolbuffers/protobuf
PROTOC_VERSION            ?= v21.1
# https://pkg.go.dev/google.golang.org/protobuf
PROTOC_GEN_GO_VERSION     ?= v1.28.0
# https://pkg.go.dev/google.golang.org/grpc/cmd/protoc-gen-go-grpc
PROTOC_GEN_GO_GRPC_VERSION?= v1.2.0
# Git2Go and libgit2 may need to be updated in sync. Please refer to
# https://github.com/libgit2/git2go/#which-go-version-to-use for a
# compatibility matrix.
GIT2GO_VERSION            ?= v33
LIBGIT2_VERSION           ?= v1.3.0
DELVE_VERSION             ?= v1.8.3

# protoc target
PROTOC_REPO_URL ?= https://github.com/protocolbuffers/protobuf
PROTOC_SOURCE_DIR   ?= ${DEPENDENCY_DIR}/protobuf/source
PROTOC_BUILD_DIR    ?= ${DEPENDENCY_DIR}/protobuf/build
PROTOC_INSTALL_DIR  ?= ${DEPENDENCY_DIR}/protobuf/install

ifeq ($(origin PROTOC_BUILD_OPTIONS),undefined)
    ## Build options for protoc.
    PROTOC_BUILD_OPTIONS ?=
    PROTOC_BUILD_OPTIONS += -DBUILD_SHARED_LIBS=NO
    PROTOC_BUILD_OPTIONS += -DCMAKE_INSTALL_PREFIX=${PROTOC_INSTALL_DIR}
    PROTOC_BUILD_OPTIONS += -Dprotobuf_BUILD_TESTS=OFF
endif

# Git target
GIT_REPO_URL       ?= https://gitlab.com/gitlab-org/gitlab-git.git
# The default prefix specifies where Git will be installed to if no GIT_PREFIX
# was given. This directory will be cleaned up before we install into it.
GIT_DEFAULT_PREFIX := ${DEPENDENCY_DIR}/git/install
GIT_QUIET          :=
ifeq (${Q},@)
    GIT_QUIET = --quiet
endif

GIT_EXECUTABLES += git
GIT_EXECUTABLES += git-remote-http
GIT_EXECUTABLES += git-http-backend

# The default version is used in case the caller does not set the variable or
# if it is either set to the empty string or "default".
ifeq (${GIT_VERSION:default=},)
    override GIT_VERSION := v2.35.1

    # This extra version has two intentions: first, it allows us to detect
    # capabilities of the command at runtime. Second, it helps admins to
    # discover which version is currently in use. As such, this version must be
    # incremented whenever a new patch is added above. When no patches exist,
    # then this should be undefined. Otherwise, it must be set to at least
    # `gl1` given that `0` is the "default" GitLab patch level.
    GIT_EXTRA_VERSION := gl1

    # Before adding custom patches, please read doc/PROCESS.md#Patching-git
    # first to make sure your patches meet our acceptance criteria. Patches
    # must be put into `_support/git-patches`.
    GIT_PATCHES := $(sort $(wildcard ${SOURCE_DIR}/_support/git-patches/v2.35.1.gl1/*))
else
    # Support both vX.Y.Z and X.Y.Z version patterns, since callers across GitLab
    # use both.
    override GIT_VERSION := $(shell echo ${GIT_VERSION} | awk '/^[0-9]\.[0-9]+\.[0-9]+$$/ { printf "v" } { print $$1 }')
endif

ifeq ($(origin GIT_BUILD_OPTIONS),undefined)
    ## Build options for Git.
    GIT_BUILD_OPTIONS ?=
    # activate developer checks
    GIT_BUILD_OPTIONS += DEVELOPER=1
    # but don't cause warnings to fail the build
    GIT_BUILD_OPTIONS += DEVOPTS=no-error
    GIT_BUILD_OPTIONS += USE_LIBPCRE=YesPlease
    GIT_BUILD_OPTIONS += NO_PERL=YesPlease
    GIT_BUILD_OPTIONS += NO_EXPAT=YesPlease
    GIT_BUILD_OPTIONS += NO_TCLTK=YesPlease
    GIT_BUILD_OPTIONS += NO_GETTEXT=YesPlease
    GIT_BUILD_OPTIONS += NO_PYTHON=YesPlease
    PCRE_PC=libpcre2-8
    ifeq ($(shell pkg-config --exists ${PCRE_PC} && echo exists),exists)
	    GIT_BUILD_OPTIONS += LIBPCREDIR=$(shell pkg-config ${PCRE_PC} --variable prefix)
    endif
endif

ifdef GIT_APPEND_BUILD_OPTIONS
	GIT_BUILD_OPTIONS += ${GIT_APPEND_BUILD_OPTIONS}
endif

ifdef GIT_FIPS_BUILD_OPTIONS
	GIT_BUILD_OPTIONS += ${GIT_FIPS_BUILD_OPTIONS}
endif

# libgit2 target
LIBGIT2_REPO_URL    ?= https://gitlab.com/libgit2/libgit2
LIBGIT2_SOURCE_DIR  ?= ${DEPENDENCY_DIR}/libgit2/source
LIBGIT2_BUILD_DIR   ?= ${DEPENDENCY_DIR}/libgit2/build
LIBGIT2_INSTALL_DIR ?= ${DEPENDENCY_DIR}/libgit2/install

ifeq ($(origin LIBGIT2_BUILD_OPTIONS),undefined)
    ## Build options for libgit2.
    LIBGIT2_BUILD_OPTIONS ?=
    LIBGIT2_BUILD_OPTIONS += -DTHREADSAFE=ON
    LIBGIT2_BUILD_OPTIONS += -DBUILD_CLAR=OFF
    LIBGIT2_BUILD_OPTIONS += -DBUILD_SHARED_LIBS=OFF
    LIBGIT2_BUILD_OPTIONS += -DCMAKE_C_FLAGS=-fPIC
    LIBGIT2_BUILD_OPTIONS += -DCMAKE_BUILD_TYPE=Release
    LIBGIT2_BUILD_OPTIONS += -DCMAKE_INSTALL_PREFIX=${LIBGIT2_INSTALL_DIR}
    LIBGIT2_BUILD_OPTIONS += -DCMAKE_INSTALL_LIBDIR=lib
    LIBGIT2_BUILD_OPTIONS += -DENABLE_TRACE=OFF
    LIBGIT2_BUILD_OPTIONS += -DUSE_SSH=OFF
    LIBGIT2_BUILD_OPTIONS += -DUSE_HTTPS=OFF
    LIBGIT2_BUILD_OPTIONS += -DUSE_ICONV=OFF
    LIBGIT2_BUILD_OPTIONS += -DUSE_NTLMCLIENT=OFF
    LIBGIT2_BUILD_OPTIONS += -DUSE_BUNDLED_ZLIB=ON
    LIBGIT2_BUILD_OPTIONS += -DUSE_HTTP_PARSER=builtin
    LIBGIT2_BUILD_OPTIONS += -DREGEX_BACKEND=builtin
endif

ifdef LIBGIT2_APPEND_BUILD_OPTIONS
	LIBGIT2_BUILD_OPTIONS += ${LIBGIT2_APPEND_BUILD_OPTIONS}
endif

# These variables control test options and artifacts
## List of Go packages which shall be tested.
## Go packages to test when using the test-go target.
TEST_PACKAGES     ?= ${SOURCE_DIR}/...
## Test options passed to `go test`.
TEST_OPTIONS      ?= -count=1
## Specify the output format used to print tests ["standard-verbose", "standard-quiet", "short"]
TEST_FORMAT       ?= short
TEST_REPORT       ?= ${BUILD_DIR}/reports/go-tests-report.xml
# Full output of `go test -json`
TEST_FULL_OUTPUT  ?= /dev/null
## Specify the output directory for test coverage reports.
TEST_COVERAGE_DIR ?= ${BUILD_DIR}/cover
## Directory where all runtime test data is being created.
TEST_TMP_DIR      ?=
TEST_REPO_DIR     := ${BUILD_DIR}/testrepos
TEST_REPO         := ${TEST_REPO_DIR}/gitlab-test.git
TEST_REPO_MIRROR  := ${TEST_REPO_DIR}/gitlab-test-mirror.git
TEST_REPO_GIT     := ${TEST_REPO_DIR}/gitlab-git-test.git
BENCHMARK_REPO    := ${TEST_REPO_DIR}/benchmark.git

# All executables provided by Gitaly
GITALY_EXECUTABLES    = $(addprefix ${BUILD_DIR}/bin/,$(notdir $(shell find ${SOURCE_DIR}/cmd -mindepth 1 -maxdepth 1 -type d -print)) gitaly-git2go-v14)
# Find all Go source files.
find_go_sources       = $(shell find ${SOURCE_DIR} -type d \( -name ruby -o -name vendor -o -name testdata -o -name '_*' -o -path '*/proto/go/gitalypb' \) -prune -o -type f -name '*.go' -not -name '*.pb.go' -print | sort -u)

# run_go_tests will execute Go tests with all required parameters. Its
# behaviour can be modified via the following variables:
#
# TEST_OPTIONS: any additional options
# TEST_PACKAGES: packages which shall be tested
run_go_tests = PATH='${SOURCE_DIR}/internal/testhelper/testdata/home/bin:${PATH}' \
    TEST_TMP_DIR='${TEST_TMP_DIR}' \
    ${GOTESTSUM} --format ${TEST_FORMAT} --junitfile ${TEST_REPORT} --jsonfile ${TEST_FULL_OUTPUT} -- -ldflags '${GO_LDFLAGS}' -tags '${SERVER_BUILD_TAGS},${GIT2GO_BUILD_TAGS}' ${TEST_OPTIONS} ${TEST_PACKAGES}

## Test options passed to `dlv test`.
DEBUG_OPTIONS      ?= $(patsubst -%,-test.%,${TEST_OPTIONS})

# debug_go_tests will execute Go tests from a single package in the delve debugger.
# Its behaviour can be modified via the following variable:
#
# DEBUG_OPTIONS: any additional options, will default to TEST_OPTIONS if not set.
debug_go_tests = PATH='${SOURCE_DIR}/internal/testhelper/testdata/home/bin:${PATH}' \
    TEST_TMP_DIR='${TEST_TMP_DIR}' \
    ${DELVE} test --build-flags="-ldflags '${GO_LDFLAGS}' -tags '${SERVER_BUILD_TAGS},${GIT2GO_BUILD_TAGS}'" ${TEST_PACKAGES} -- ${DEBUG_OPTIONS}

unexport GOROOT
export GOCACHE                   ?= ${BUILD_DIR}/cache
export GOPROXY                   ?= https://proxy.golang.org
export PATH                      := ${BUILD_DIR}/bin:${PATH}
export PKG_CONFIG_PATH           := ${LIBGIT2_INSTALL_DIR}/lib/pkgconfig
# Allow the linker flag -D_THREAD_SAFE as libgit2 is compiled with it on FreeBSD
export CGO_LDFLAGS_ALLOW          = -D_THREAD_SAFE

# Disallow changes to the Gemfile
export BUNDLE_FROZEN = true

# By default, intermediate targets get deleted automatically after a successful
# build. We do not want that though: there's some precious intermediate targets
# like our `*.version` targets which are required in order to determine whether
# a dependency needs to be rebuilt. By specifying `.SECONDARY`, intermediate
# targets will never get deleted automatically.
.SECONDARY:

.PHONY: all
## Default target which builds Gitaly.
all: build

.PHONY: .FORCE
.FORCE:

## Print help about available targets and variables.
help:
	@echo "usage: make [<target>...] [<variable>=<value>...]"
	@echo ""
	@echo "These are the available targets:"
	@echo ""

	@ # Match all targets which have preceding `## ` comments.
	${Q}awk '/^## / { sub(/^##/, "", $$0) ; desc = desc $$0 ; next } \
		 /^[[:alpha:]][[:alnum:]_-]+:/ && desc { print "  " $$1 desc } \
		 { desc = "" }' $(MAKEFILE_LIST) | sort | column -s: -t

	${Q}echo ""
	${Q}echo "These are common variables which can be overridden in config.mak:"
	${Q}echo ""

	@ # Match all variables which have preceding `## ` comments and which are assigned via `?=`.
	${Q}awk '/^[[:space:]]*## / { sub(/^[[:space:]]*##/,"",$$0) ; desc = desc $$0 ; next } \
		 /^[[:space:]]*[[:alpha:]][[:alnum:]_-]+[[:space:]]*\?=/ && desc { print "  "$$1 ":" desc } \
		 { desc = "" }' $(MAKEFILE_LIST) | sort | column -s: -t

.PHONY: build
## Build Go binaries and install required Ruby Gems.
build: ${SOURCE_DIR}/.ruby-bundle ${GITALY_EXECUTABLES}

.PHONY: install
## Install Gitaly binaries. The target directory can be modified by setting PREFIX and DESTDIR.
install: build
	${Q}mkdir -p ${INSTALL_DEST_DIR}
	install ${GITALY_EXECUTABLES} "${INSTALL_DEST_DIR}"

.PHONY: build-bundled-git
## Build bundled Git binaries.
build-bundled-git: build-bundled-git-v2.35.1.gl1
build-bundled-git: build-bundled-git-v2.37.1.gl1
build-bundled-git-v2.35.1.gl1: $(patsubst %,${BUILD_DIR}/bin/gitaly-%-v2.35.1.gl1,${GIT_EXECUTABLES})
build-bundled-git-v2.37.1.gl1: $(patsubst %,${BUILD_DIR}/bin/gitaly-%-v2.37.1.gl1,${GIT_EXECUTABLES})

.PHONY: install-bundled-git
## Install bundled Git binaries. The target directory can be modified by
## setting PREFIX and DESTDIR.
install-bundled-git: install-bundled-git-v2.35.1.gl1
install-bundled-git: install-bundled-git-v2.37.1.gl1
install-bundled-git-v2.35.1.gl1: $(patsubst %,${INSTALL_DEST_DIR}/gitaly-%-v2.35.1.gl1,${GIT_EXECUTABLES})
install-bundled-git-v2.37.1.gl1: $(patsubst %,${INSTALL_DEST_DIR}/gitaly-%-v2.37.1.gl1,${GIT_EXECUTABLES})

ifdef WITH_BUNDLED_GIT
build: build-bundled-git
prepare-tests: build-bundled-git
install: install-bundled-git

export GITALY_TESTING_BUNDLED_GIT_PATH ?= ${BUILD_DIR}/bin
else
prepare-tests: git

export GITALY_TESTING_GIT_BINARY ?= ${GIT_PREFIX}/bin/git
endif

.PHONY: prepare-tests
prepare-tests: libgit2 prepare-test-repos ${SOURCE_DIR}/.ruby-bundle ${GOTESTSUM}
	${Q}mkdir -p "$(dir ${TEST_REPORT})"

.PHONY: prepare-debug
prepare-debug: ${DELVE}

.PHONY: prepare-test-repos
prepare-test-repos: ${TEST_REPO_MIRROR} ${TEST_REPO} ${TEST_REPO_GIT}

.PHONY: test
## Run Go and Ruby tests.
test: test-go test-ruby

.PHONY: test-ruby
test-ruby: rspec

.PHONY: test-go
## Run Go tests.
test-go: prepare-tests
	${Q}$(call run_go_tests)

.PHONY: debug-go-tests
## Run Go tests in delve debugger.
debug-test-go: prepare-tests prepare-debug
	${Q}$(call debug_go_tests)

.PHONY: test
## Run Go benchmarks.
bench: TEST_OPTIONS := ${TEST_OPTIONS} -bench=. -run=^$
bench: ${BENCHMARK_REPO} test-go

.PHONY: test-with-proxies
test-with-proxies: TEST_OPTIONS  := ${TEST_OPTIONS} -exec ${SOURCE_DIR}/_support/bad-proxies
test-with-proxies: TEST_PACKAGES := ${GITALY_PACKAGE}/internal/gitaly/rubyserver
test-with-proxies: prepare-tests
	${Q}$(call run_go_tests)

.PHONY: test-with-praefect
## Run Go tests with Praefect.
test-with-praefect: prepare-tests
	${Q}GITALY_TEST_WITH_PRAEFECT=YesPlease $(call run_go_tests)

.PHONY: race-go
## Run Go tests with race detection enabled.
race-go: TEST_OPTIONS := ${TEST_OPTIONS} -race
race-go: test-go

.PHONY: rspec
## Run Ruby tests.
rspec: prepare-tests
	${Q}cd ${GITALY_RUBY_DIR} && PATH='${SOURCE_DIR}/internal/testhelper/testdata/home/bin:${PATH}' bundle exec rspec

# This is a workaround for our unprivileged CI builds. We manually execute the
# build target as privileged user, but then run the rspec target unprivileged.
# We thus cannot rebuild binaries there given that we have no permissions to
# write into the build directory.
ifndef SKIP_RSPEC_BUILD
rspec: build
endif

.PHONY: verify
## Verify that various files conform to our expectations.
verify: check-mod-tidy notice-up-to-date check-proto rubocop lint

.PHONY: check-mod-tidy
check-mod-tidy:
	${Q}${GIT} diff --quiet --exit-code go.mod go.sum || (echo "error: uncommitted changes in go.mod or go.sum" && exit 1)
	${Q}go mod tidy
	${Q}${GIT} diff --quiet --exit-code go.mod go.sum || (echo "error: uncommitted changes in go.mod or go.sum" && exit 1)

.PHONY: lint
## Run Go linter.
lint: ${GOLANGCI_LINT} libgit2
	${Q}${GOLANGCI_LINT} run --build-tags "${SERVER_BUILD_TAGS},${GIT2GO_BUILD_TAGS}" --out-format tab --config ${GOLANGCI_LINT_CONFIG} ${GOLANGCI_LINT_OPTIONS}

.PHONY: format
## Run Go formatter and adjust imports.
format: ${GOIMPORTS} ${GOFUMPT}
	${Q}${GOIMPORTS} -w -l $(call find_go_sources)
	${Q}${GOFUMPT} -w $(call find_go_sources)

.PHONY: notice-up-to-date
notice-up-to-date: ${BUILD_DIR}/NOTICE
	${Q}(cmp ${BUILD_DIR}/NOTICE ${SOURCE_DIR}/NOTICE) || (echo >&2 "NOTICE requires update: 'make notice'" && false)

.PHONY: notice
## Regenerate the NOTICE file.
notice: ${SOURCE_DIR}/NOTICE

.PHONY: clean
## Clean up build artifacts.
clean:
	rm -rf ${BUILD_DIR} ${SOURCE_DIR}/ruby/.bundle/ ${SOURCE_DIR}/ruby/vendor/bundle/

.PHONY: clean-ruby-vendor-go
clean-ruby-vendor-go:
	mkdir -p ${SOURCE_DIR}/ruby/vendor && find ${SOURCE_DIR}/ruby/vendor -type f -name '*.go' -delete

.PHONY: rubocop
## Run Rubocop.
rubocop: ${SOURCE_DIR}/.ruby-bundle
	${Q}cd ${GITALY_RUBY_DIR} && bundle exec rubocop --parallel --config ${GITALY_RUBY_DIR}/.rubocop.yml ${GITALY_RUBY_DIR} ${SOURCE_DIR}/_support/test-boot

.PHONY: cover
## Generate coverage report via Go tests.
cover: TEST_OPTIONS  := ${TEST_OPTIONS} -coverprofile "${TEST_COVERAGE_DIR}/all.merged"
cover: prepare-tests libgit2 ${GOCOVER_COBERTURA}
	${Q}rm -rf "${TEST_COVERAGE_DIR}"
	${Q}mkdir -p "${TEST_COVERAGE_DIR}"
	${Q}$(call run_go_tests)
	${Q}go tool cover -html  "${TEST_COVERAGE_DIR}/all.merged" -o "${TEST_COVERAGE_DIR}/all.html"
	@ # sed is used below to convert file paths to repository root relative paths. See https://gitlab.com/gitlab-org/gitlab/-/issues/217664
	${Q}${GOCOVER_COBERTURA} <"${TEST_COVERAGE_DIR}/all.merged" | sed 's;filename=\"$(shell go list -m)/;filename=\";g' >"${TEST_COVERAGE_DIR}/cobertura.xml"
	${Q}echo ""
	${Q}echo "=====> Total test coverage: <====="
	${Q}echo ""
	${Q}go tool cover -func "${TEST_COVERAGE_DIR}/all.merged"

.PHONY: proto
## Regenerate protobuf definitions.
proto: SHARED_PROTOC_OPTS = --plugin=${PROTOC_GEN_GO} --plugin=${PROTOC_GEN_GO_GRPC} --plugin=${PROTOC_GEN_GITALY_PROTOLIST} --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative
proto: ${PROTOC} ${PROTOC_GEN_GO} ${PROTOC_GEN_GO_GRPC} ${PROTOC_GEN_GITALY_PROTOLIST} ${SOURCE_DIR}/.ruby-bundle
	${Q}mkdir -p ${SOURCE_DIR}/proto/go/gitalypb
	${Q}rm -f ${SOURCE_DIR}/proto/go/gitalypb/*.pb.go
	${PROTOC} ${SHARED_PROTOC_OPTS} -I ${SOURCE_DIR}/proto -I ${PROTOC_INSTALL_DIR}/include --go_out=${SOURCE_DIR}/proto/go/gitalypb --gitaly-protolist_out=proto_dir=${SOURCE_DIR}/proto,gitalypb_dir=${SOURCE_DIR}/proto/go/gitalypb:${SOURCE_DIR} --go-grpc_out=${SOURCE_DIR}/proto/go/gitalypb ${SOURCE_DIR}/proto/*.proto
	${SOURCE_DIR}/_support/generate-proto-ruby
	@ # this part is related to the generation of sources from testing proto files
	${PROTOC} ${SHARED_PROTOC_OPTS} -I ${SOURCE_DIR}/internal --go_out=${SOURCE_DIR}/internal --go-grpc_out=${SOURCE_DIR}/internal ${SOURCE_DIR}/internal/praefect/grpc-proxy/testdata/test.proto
	${PROTOC} ${SHARED_PROTOC_OPTS} -I ${SOURCE_DIR}/proto -I ${SOURCE_DIR}/internal -I ${PROTOC_INSTALL_DIR}/include --go_out=${SOURCE_DIR}/internal --go-grpc_out=${SOURCE_DIR}/internal \
		${SOURCE_DIR}/internal/praefect/mock/mock.proto \
		${SOURCE_DIR}/internal/middleware/cache/testdata/stream.proto \
		${SOURCE_DIR}/internal/helper/chunk/testdata/test.proto \
		${SOURCE_DIR}/internal/middleware/limithandler/testdata/test.proto
	${PROTOC} ${SHARED_PROTOC_OPTS} -I ${SOURCE_DIR}/proto -I ${SOURCE_DIR}/tools -I ${PROTOC_INSTALL_DIR}/include --go_out=${SOURCE_DIR}/tools --go-grpc_out=${SOURCE_DIR}/tools ${SOURCE_DIR}/tools/protoc-gen-gitaly-lint/testdata/*.proto

.PHONY: check-proto
check-proto: proto no-proto-changes lint-proto

.PHONY: lint-proto
lint-proto: ${PROTOC} ${PROTOLINT} ${PROTOC_GEN_GITALY_LINT}
	${Q}${PROTOC} -I ${SOURCE_DIR}/proto -I ${PROTOC_INSTALL_DIR}/include --plugin=${PROTOC_GEN_GITALY_LINT} --gitaly-lint_out=${SOURCE_DIR} ${SOURCE_DIR}/proto/*.proto
	${Q}${PROTOLINT} lint -config_dir_path=${SOURCE_DIR}/proto ${SOURCE_DIR}/proto

.PHONY: no-changes
no-changes:
	${Q}${GIT} diff --exit-code

.PHONY: no-proto-changes
no-proto-changes: | ${BUILD_DIR}
	${Q}${GIT} diff --exit-code -- '*.pb.go' 'ruby/proto/gitaly'

.PHONY: dump-database-schema
## Dump the clean database schema of Praefect into a file.
dump-database-schema: build
	${Q}"${SOURCE_DIR}"/_support/generate-praefect-schema >"${SOURCE_DIR}"/_support/praefect-schema.sql

.PHONY: upgrade-module
upgrade-module:
	${Q}go run ${SOURCE_DIR}/tools/module-updater/main.go -dir . -from=${FROM_MODULE} -to=${TO_MODULE}
	${Q}${MAKE} proto

.PHONY: git
## Build Git.
git: ${GIT_PREFIX}/bin/git

.PHONY: libgit2
## Build libgit2.
libgit2: ${LIBGIT2_INSTALL_DIR}/lib/libgit2.a

# This file is used by Omnibus and CNG to skip the "bundle install"
# step. Both Omnibus and CNG assume it is in the Gitaly root, not in
# _build. Hence the '../' in front.
${SOURCE_DIR}/.ruby-bundle: ${GITALY_RUBY_DIR}/Gemfile.lock ${GITALY_RUBY_DIR}/Gemfile
	${Q}cd ${GITALY_RUBY_DIR} && bundle install
	${Q}touch $@

${SOURCE_DIR}/NOTICE: ${BUILD_DIR}/NOTICE
	${Q}mv $< $@

${BUILD_DIR}/NOTICE: ${GO_LICENSES} clean-ruby-vendor-go
	${Q}rm -rf ${BUILD_DIR}/licenses
	${Q}GOOS=linux GOFLAGS="-tags=${SERVER_BUILD_TAGS},${GIT2GO_BUILD_TAGS}" ${GO_LICENSES} save ${SOURCE_DIR}/... --save_path=${BUILD_DIR}/licenses
	${Q}go run ${SOURCE_DIR}/tools/noticegen/noticegen.go -source ${BUILD_DIR}/licenses -template ${SOURCE_DIR}/tools/noticegen/notice.template > ${BUILD_DIR}/NOTICE

${BUILD_DIR}:
	${Q}mkdir -p ${BUILD_DIR}
${BUILD_DIR}/bin: | ${BUILD_DIR}
	${Q}mkdir -p ${BUILD_DIR}/bin
${TOOLS_DIR}: | ${BUILD_DIR}
	${Q}mkdir -p ${TOOLS_DIR}
${DEPENDENCY_DIR}: | ${BUILD_DIR}
	${Q}mkdir -p ${DEPENDENCY_DIR}

# This target builds a full Git distribution and installs it into GIT_PREFIX.
${GIT_PREFIX}/bin/git: ${DEPENDENCY_DIR}/git-distribution/Makefile
	@ # Remove the Git installation first in case GIT_PREFIX is the default
	@ # prefix which always points into our build directory. This is done so
	@ # we never end up with mixed Git installations on developer machines.
	@ # We cannot ever remove GIT_PREFIX though in case they're different
	@ # because it may point to a user-controlled directory.
	${Q}if [ "x${GIT_DEFAULT_PREFIX}" = "x${GIT_PREFIX}" ]; then rm -rf "${GIT_DEFAULT_PREFIX}"; fi
	${Q}env -u PROFILE -u MAKEFLAGS -u GIT_VERSION ${MAKE} -C "$(<D)" -j$(shell nproc) prefix=${GIT_PREFIX} ${GIT_BUILD_OPTIONS} install
	${Q}touch $@

${BUILD_DIR}/bin/gitaly-%-v2.35.1.gl1: override GIT_PATCHES := $(sort $(wildcard ${SOURCE_DIR}/_support/git-patches/v2.35.1.gl1/*))
${BUILD_DIR}/bin/gitaly-%-v2.35.1.gl1: override GIT_VERSION = v2.35.1
${BUILD_DIR}/bin/gitaly-%-v2.35.1.gl1: override GIT_EXTRA_VERSION = gl1
${BUILD_DIR}/bin/gitaly-%-v2.35.1.gl1: ${DEPENDENCY_DIR}/git-v2.35.1.gl1/% | ${BUILD_DIR}/bin
	${Q}install $< $@

${BUILD_DIR}/bin/gitaly-%-v2.37.1.gl1: override GIT_PATCHES := $(sort $(wildcard ${SOURCE_DIR}/_support/git-patches/v2.37.1.gl1/*))
${BUILD_DIR}/bin/gitaly-%-v2.37.1.gl1: override GIT_VERSION = v2.37.1
${BUILD_DIR}/bin/gitaly-%-v2.37.1.gl1: override GIT_EXTRA_VERSION = gl1
${BUILD_DIR}/bin/gitaly-%-v2.37.1.gl1: ${DEPENDENCY_DIR}/git-v2.37.1.gl1/% | ${BUILD_DIR}/bin
	${Q}install $< $@

${BUILD_DIR}/bin/%: ${BUILD_DIR}/intermediate/% | ${BUILD_DIR}/bin
	@ # To compute a unique and deterministic value for GNU build-id, we use an
	@ # intermediate binary which has a fixed build ID of "TEMP_GITALY_BUILD_ID",
	@ # which we replace with a deterministic build ID derived from the Go build ID.
	@ # If we cannot extract a Go build-id, we punt and fallback to using a random 32-byte hex string.
	@ # This fallback is unique but non-deterministic, making it sufficient to avoid generating the
	@ # GNU build-id from the empty string and causing guaranteed collisions.
	${Q}GO_BUILD_ID=$$(go tool buildid "$<" || openssl rand -hex 32) && \
	GNU_BUILD_ID=$$(echo $$GO_BUILD_ID | sha1sum | cut -d' ' -f1) && \
	if test "${OS}" = "Linux"; then \
		go run "${SOURCE_DIR}"/tools/replace-buildid \
			-input "$<" -input-build-id "${TEMPORARY_BUILD_ID}" \
			-output "$@" -output-build-id "$$GNU_BUILD_ID"; \
	else \
		install "$<" "$@"; \
	fi

${BUILD_DIR}/intermediate/gitaly:            GO_BUILD_TAGS = ${SERVER_BUILD_TAGS}
${BUILD_DIR}/intermediate/praefect:          GO_BUILD_TAGS = ${SERVER_BUILD_TAGS}
${BUILD_DIR}/intermediate/gitaly-git2go-v15: GO_BUILD_TAGS = ${GIT2GO_BUILD_TAGS}
${BUILD_DIR}/intermediate/gitaly-git2go-v15: libgit2
${BUILD_DIR}/intermediate/%: .FORCE
	@ # We're building intermediate binaries first which contain a fixed build ID
	@ # of "TEMP_GITALY_BUILD_ID". In the final binary we replace this build ID with
	@ # the computed build ID for this binary.
	${Q}go build -o "$@" -ldflags '-B 0x${TEMPORARY_BUILD_ID} ${GO_LDFLAGS}' -tags "${GO_BUILD_TAGS}" $(addprefix ${SOURCE_DIR}/cmd/,$(@F))

# This target is required for backwards compatibility during zero-downtime
# upgrades and can be removed when v15.0 has been released.
${BUILD_DIR}/intermediate/gitaly-git2go-v14: GO_BUILD_TAGS = ${GIT2GO_BUILD_TAGS}
${BUILD_DIR}/intermediate/gitaly-git2go-v14: libgit2
${BUILD_DIR}/intermediate/gitaly-git2go-v14: .FORCE
	@ # gitaly-git2go-v14  pulls directly from a commit sha so that the gitaly-git2go-v14
	@ # binary can continue to be installed for the sake of zero downtime
	@ # upgrades.
	${Q}GOBIN="$(dir $@)" go install -ldflags '-B 0x${TEMPORARY_BUILD_ID} ${GO_LDFLAGS}' -tags "${GO_BUILD_TAGS}" "gitlab.com/gitlab-org/gitaly/v14/cmd/gitaly-git2go-v14@c7c7c936c302ab435a0a56fbc19cfbd9bea0c835"

# This is a build hack to avoid excessive rebuilding of targets. Instead of
# depending on the Makefile, we start to depend on tool versions as defined in
# the Makefile. Like this, we only rebuild if the tool versions actually
# change. The dependency on the phony target is required to always rebuild
# these targets.
.PHONY: dependency-version
${DEPENDENCY_DIR}/libgit2.version: dependency-version | ${DEPENDENCY_DIR}
	${Q}[ x"$$(cat "$@" 2>/dev/null)" = x"${LIBGIT2_VERSION} ${LIBGIT2_BUILD_OPTIONS}" ] || >$@ echo -n "${LIBGIT2_VERSION} ${LIBGIT2_BUILD_OPTIONS}"
${DEPENDENCY_DIR}/git-%.version: dependency-version | ${DEPENDENCY_DIR}
	${Q}[ x"$$(cat "$@" 2>/dev/null)" = x"${GIT_VERSION}.${GIT_EXTRA_VERSION} ${GIT_BUILD_OPTIONS} ${GIT_PATCHES}" ] || >$@ echo -n "${GIT_VERSION}.${GIT_EXTRA_VERSION} ${GIT_BUILD_OPTIONS} ${GIT_PATCHES}"
${DEPENDENCY_DIR}/protoc.version: dependency-version | ${DEPENDENCY_DIR}
	${Q}[ x"$$(cat "$@" 2>/dev/null)" = x"${PROTOC_VERSION} ${PROTOC_BUILD_OPTIONS}" ] || >$@ echo -n "${PROTOC_VERSION} ${PROTOC_BUILD_OPTIONS}"
${TOOLS_DIR}/%.version: dependency-version | ${TOOLS_DIR}
	${Q}[ x"$$(cat "$@" 2>/dev/null)" = x"${TOOL_VERSION}" ] || >$@ echo -n "${TOOL_VERSION}"

${LIBGIT2_INSTALL_DIR}/lib/libgit2.a: ${DEPENDENCY_DIR}/libgit2.version
	${Q}${GIT} -c init.defaultBranch=master init ${GIT_QUIET} ${LIBGIT2_SOURCE_DIR}
	${Q}${GIT} -C "${LIBGIT2_SOURCE_DIR}" config remote.origin.url ${LIBGIT2_REPO_URL}
	${Q}${GIT} -C "${LIBGIT2_SOURCE_DIR}" config remote.origin.tagOpt --no-tags
	${Q}${GIT} -C "${LIBGIT2_SOURCE_DIR}" fetch --depth 1 ${GIT_QUIET} origin ${LIBGIT2_VERSION}
	${Q}${GIT} -C "${LIBGIT2_SOURCE_DIR}" checkout ${GIT_QUIET} --detach FETCH_HEAD
	${Q}rm -rf ${LIBGIT2_BUILD_DIR}
	${Q}mkdir -p ${LIBGIT2_BUILD_DIR}
	${Q}cd ${LIBGIT2_BUILD_DIR} && cmake ${LIBGIT2_SOURCE_DIR} ${LIBGIT2_BUILD_OPTIONS}
	${Q}CMAKE_BUILD_PARALLEL_LEVEL=$(shell nproc) cmake --build ${LIBGIT2_BUILD_DIR} --target install
	go install -a github.com/libgit2/git2go/${GIT2GO_VERSION}

# This target is responsible for checking out Git sources. In theory, we'd only
# need to depend on the source directory. But given that the source directory
# always changes when anything inside of it changes, like when we for example
# build binaries inside of it, we cannot depend on it directly or we'd
# otherwise try to rebuild all targets depending on it whenever we build
# something else. We thus depend on the Makefile instead.
${DEPENDENCY_DIR}/git-%/Makefile: ${DEPENDENCY_DIR}/git-%.version
	${Q}${GIT} -c init.defaultBranch=master init ${GIT_QUIET} "${@D}"
	${Q}${GIT} -C "${@D}" config remote.origin.url ${GIT_REPO_URL}
	${Q}${GIT} -C "${@D}" config remote.origin.tagOpt --no-tags
	${Q}${GIT} -C "${@D}" fetch --depth 1 ${GIT_QUIET} origin ${GIT_VERSION}
	${Q}${GIT} -C "${@D}" reset --hard
	${Q}${GIT} -C "${@D}" checkout ${GIT_QUIET} --detach FETCH_HEAD
	${Q}if test -n "${GIT_PATCHES}"; then ${GIT} -C "${@D}" apply ${GIT_PATCHES}; fi
	@ # We're writing the version into the "version" file in Git's own source
	@ # directory. If it exists, Git's Makefile will pick it up and use it as
	@ # the version instead of auto-detecting via git-describe(1).
	${Q}if test -n "${GIT_EXTRA_VERSION}"; then echo ${GIT_VERSION}.${GIT_EXTRA_VERSION} >"${@D}"/version; else rm -f "${@D}"/version; fi
	${Q}touch $@

$(patsubst %,${DEPENDENCY_DIR}/git-\%/%,${GIT_EXECUTABLES}): ${DEPENDENCY_DIR}/git-%/Makefile
	${Q}env -u PROFILE -u MAKEFLAGS -u GIT_VERSION ${MAKE} -C "${@D}" -j$(shell nproc) prefix=${GIT_PREFIX} ${GIT_BUILD_OPTIONS} ${GIT_EXECUTABLES}
	${Q}touch $@

${INSTALL_DEST_DIR}/gitaly-%: ${BUILD_DIR}/bin/gitaly-%
	${Q}mkdir -p ${@D}
	${Q}install $< $@

${PROTOC}: ${DEPENDENCY_DIR}/protoc.version | ${TOOLS_DIR}
	${Q}${GIT} -c init.defaultBranch=master init ${GIT_QUIET} "${PROTOC_SOURCE_DIR}"
	${Q}${GIT} -C "${PROTOC_SOURCE_DIR}" config remote.origin.url ${PROTOC_REPO_URL}
	${Q}${GIT} -C "${PROTOC_SOURCE_DIR}" config remote.origin.tagOpt --no-tags
	${Q}${GIT} -C "${PROTOC_SOURCE_DIR}" fetch --depth 1 ${GIT_QUIET} origin ${PROTOC_VERSION}
	${Q}${GIT} -C "${PROTOC_SOURCE_DIR}" checkout ${GIT_QUIET} --detach FETCH_HEAD
	${Q}${GIT} -C "${PROTOC_SOURCE_DIR}" submodule update --init --recursive
	${Q}rm -rf "${PROTOC_BUILD_DIR}"
	${Q}cmake "${PROTOC_SOURCE_DIR}" -B "${PROTOC_BUILD_DIR}" ${PROTOC_BUILD_OPTIONS}
	${Q}cmake --build "${PROTOC_BUILD_DIR}" --target install -- -j $(shell nproc)
	${Q}cp "${PROTOC_INSTALL_DIR}"/bin/protoc ${PROTOC}

${TOOLS_DIR}/%: ${TOOLS_DIR}/%.version
	${Q}GOBIN=${TOOLS_DIR} go install ${TOOL_PACKAGE}@${TOOL_VERSION}

${PROTOC_GEN_GITALY_LINT}: proto | ${TOOLS_DIR}
	${Q}go build -o $@ ${SOURCE_DIR}/tools/protoc-gen-gitaly-lint

${PROTOC_GEN_GITALY_PROTOLIST}: | ${TOOLS_DIR}
	${Q}go build -o $@ ${SOURCE_DIR}/tools/protoc-gen-gitaly-protolist

# External tools
${GOCOVER_COBERTURA}: TOOL_PACKAGE = github.com/t-yuki/gocover-cobertura
${GOCOVER_COBERTURA}: TOOL_VERSION = ${GOCOVER_COBERTURA_VERSION}
${GOFUMPT}:           TOOL_PACKAGE = mvdan.cc/gofumpt
${GOFUMPT}:           TOOL_VERSION = ${GOFUMPT_VERSION}
${GOIMPORTS}:         TOOL_PACKAGE = golang.org/x/tools/cmd/goimports
${GOIMPORTS}:         TOOL_VERSION = ${GOIMPORTS_VERSION}
${GOLANGCI_LINT}:     TOOL_PACKAGE = github.com/golangci/golangci-lint/cmd/golangci-lint
${GOLANGCI_LINT}:     TOOL_VERSION = ${GOLANGCI_LINT_VERSION}
${PROTOLINT}:         TOOL_PACKAGE = github.com/yoheimuta/protolint/cmd/protolint
${PROTOLINT}:         TOOL_VERSION = ${PROTOLINT_VERSION}
${GOTESTSUM}:         TOOL_PACKAGE = gotest.tools/gotestsum
${GOTESTSUM}:         TOOL_VERSION = ${GOTESTSUM_VERSION}
${GO_LICENSES}:       TOOL_PACKAGE = github.com/google/go-licenses
${GO_LICENSES}:       TOOL_VERSION = ${GO_LICENSES_VERSION}
${PROTOC_GEN_GO}:     TOOL_PACKAGE = google.golang.org/protobuf/cmd/protoc-gen-go
${PROTOC_GEN_GO}:     TOOL_VERSION = ${PROTOC_GEN_GO_VERSION}
${PROTOC_GEN_GO_GRPC}:TOOL_PACKAGE = google.golang.org/grpc/cmd/protoc-gen-go-grpc
${PROTOC_GEN_GO_GRPC}:TOOL_VERSION = ${PROTOC_GEN_GO_GRPC_VERSION}
${DELVE}:             TOOL_PACKAGE = github.com/go-delve/delve/cmd/dlv
${DELVE}:             TOOL_VERSION = ${DELVE_VERSION}

${TEST_REPO_MIRROR}:
	${GIT} clone --mirror ${GIT_QUIET} https://gitlab.com/gitlab-org/gitlab-test.git $@

${TEST_REPO}:
	${GIT} clone --bare ${GIT_QUIET} https://gitlab.com/gitlab-org/gitlab-test.git $@
	@ # Git notes aren't fetched by default with git clone
	${GIT} -C $@ fetch ${GIT_QUIET} origin refs/notes/*:refs/notes/*
	${Q}rm -rf $@/refs
	${Q}mkdir -p $@/refs/heads $@/refs/tags
	${Q}cp ${SOURCE_DIR}/_support/gitlab-test.git-packed-refs $@/packed-refs
	${Q}${GIT} -C $@ fsck --no-progress --no-dangling

${TEST_REPO_GIT}:
	${GIT} clone --bare ${GIT_QUIET} https://gitlab.com/gitlab-org/gitlab-git-test.git $@
	${Q}rm -rf $@/refs
	${Q}mkdir -p $@/refs/heads $@/refs/tags
	${Q}cp ${SOURCE_DIR}/_support/gitlab-git-test.git-packed-refs $@/packed-refs
	${Q}${GIT} -C $@ fsck --no-progress --no-dangling

${BENCHMARK_REPO}:
	${GIT} clone --bare ${GIT_QUIET} https://gitlab.com/gitlab-org/gitlab.git $@
