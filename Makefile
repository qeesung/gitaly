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
PROTO_DEST_DIR   := ${SOURCE_DIR}/proto/go
DEPENDENCY_DIR   := ${BUILD_DIR}/deps
TOOLS_DIR        := ${BUILD_DIR}/tools

# These variables may be overridden at runtime by top-level make
## The prefix where Gitaly binaries will be installed to. Binaries will end up
## in ${PREFIX}/bin by default.
PREFIX           ?= /usr/local
prefix           ?= ${PREFIX}
exec_prefix      ?= ${prefix}
bindir           ?= ${exec_prefix}/bin
INSTALL_DEST_DIR := ${DESTDIR}${bindir}
## The prefix where Git will be installed to.
GIT_PREFIX       ?= ${PREFIX}

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
PROTOC_GEN_DOC              := ${TOOLS_DIR}/protoc-gen-doc
GOTESTSUM                   := ${TOOLS_DIR}/gotestsum
GOCOVER_COBERTURA           := ${TOOLS_DIR}/gocover-cobertura
DELVE                       := ${TOOLS_DIR}/dlv
GOVULNCHECK                 := ${TOOLS_DIR}/govulncheck

# Tool options
GOLANGCI_LINT_OPTIONS ?=
GOLANGCI_LINT_CONFIG  ?= ${SOURCE_DIR}/.golangci.yml

# Build information
GITALY_PACKAGE    := $(shell go list -m 2>/dev/null || echo unknown)
GITALY_VERSION    := $(shell ${GIT} describe --match v* 2>/dev/null | sed 's/^v//' || cat ${SOURCE_DIR}/VERSION 2>/dev/null || echo unknown)
GO_LDFLAGS        := -X ${GITALY_PACKAGE}/internal/version.version=${GITALY_VERSION}
SERVER_BUILD_TAGS := tracer_static,tracer_static_jaeger,tracer_static_stackdriver,continuous_profiler_stackdriver

# Temporary GNU build ID used as a placeholder value so that we can replace it
# with our own one after binaries have been built. This is the ASCII encoding
# of the string "TEMP_GITALY_BUILD_ID".
TEMPORARY_BUILD_ID := 54454D505F474954414C595F4255494C445F4944

## FIPS_MODE controls whether to build Gitaly and dependencies in FIPS mode.
## Set this to a non-empty value to enable it.
FIPS_MODE ?=

ifdef FIPS_MODE
    SERVER_BUILD_TAGS := ${SERVER_BUILD_TAGS},fips

    # Build Git with the OpenSSL backend for SHA256 in case FIPS-mode is
    # requested. Note that we explicitly don't do the same for SHA1: we
    # instead use SHA1DC to protect users against the SHAttered attack.
    GIT_FIPS_BUILD_OPTIONS := OPENSSL_SHA256=YesPlease

    # Go 1.19+ now requires GOEXPERIMENT=boringcrypto for FIPS compilation.
    # See https://github.com/golang/go/issues/51940 for more details.
    BORINGCRYPTO_SUPPORT := $(shell GOEXPERIMENT=boringcrypto go version > /dev/null 2>&1; echo $$?)
    ifeq ($(BORINGCRYPTO_SUPPORT), 0)
        export GOEXPERIMENT=boringcrypto
    endif

    export GITALY_TESTING_ENABLE_FIPS := YesPlease
endif

# protoc target
PROTOC_VERSION      ?= v23.1
PROTOC_REPO_URL     ?= https://github.com/protocolbuffers/protobuf
PROTOC_SOURCE_DIR   ?= ${DEPENDENCY_DIR}/protobuf/source
PROTOC_BUILD_DIR    ?= ${DEPENDENCY_DIR}/protobuf/build
PROTOC_INSTALL_DIR  ?= ${DEPENDENCY_DIR}/protobuf/install

ifeq ($(origin PROTOC_BUILD_OPTIONS),undefined)
    ## Build options for protoc.
    PROTOC_BUILD_OPTIONS ?=
    PROTOC_BUILD_OPTIONS += -DBUILD_SHARED_LIBS=NO
    PROTOC_BUILD_OPTIONS += -DCMAKE_INSTALL_PREFIX=${PROTOC_INSTALL_DIR}
    PROTOC_BUILD_OPTIONS += -Dprotobuf_BUILD_TESTS=OFF
    PROTOC_BUILD_OPTIONS += -DCMAKE_CXX_STANDARD=14
endif

# Git target
GIT_REPO_URL       ?= https://gitlab.com/gitlab-org/git.git
GIT_QUIET          :=
ifeq (${Q},@)
    GIT_QUIET = --quiet
endif

GIT_EXECUTABLES += git
GIT_EXECUTABLES += git-remote-http
GIT_EXECUTABLES += git-http-backend

## The version of Git to build and test Gitaly with when not used
## WITH_BUNDLED_GIT=YesPlease. Can be set to an arbitrary Git revision with
## tags, branches, and commit ids.
GIT_VERSION ?=
## The Git version used for bundled Git v2.44.
GIT_VERSION_2_44 ?= v2.44.2.gl1
## The Git version used for bundled Git v2.45.
GIT_VERSION_2_45 ?= v2.45.2

## Override the Git version with a custom version if specified. If set to empty
## we do not add the version file and instead use the Git version as specified i
## the Git sources. This is useful to build upon Git features which aren't yet
## part of a release or a version that cannot be parsed by Gitaly (custom refs).
OVERRIDE_GIT_VERSION ?= ${GIT_VERSION}

# The default version is used in case the caller does not set the variable or
# if it is either set to the empty string or "default".
ifeq (${GIT_VERSION:default=},)
    override GIT_VERSION := ${GIT_VERSION_2_44}
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

# git-filter-repo target
GIT_FILTER_REPO                      ?= ${BUILD_DIR}/bin/git-filter-repo
GIT_FILTER_REPO_VERSION              ?= v2.38.0
GIT_FILTER_REPO_REPO_URL             ?= https://github.com/newren/git-filter-repo
GIT_FILTER_REPO_SOURCE_DIR           ?= ${DEPENDENCY_DIR}/git-filter-repo

# These variables control test options and artifacts
## List of Go packages which shall be tested.
## Go packages to test when using the test-go target.
TEST_PACKAGES     ?= ${SOURCE_DIR}/...
## Test options passed to `go test`.
TEST_OPTIONS      ?= -count=1
## Specify the output format used to print tests ["standard-verbose", "standard-quiet", "short"]
TEST_FORMAT       ?= short
## Specify the location where the JUnit-style format shall be written to.
TEST_JUNIT_REPORT ?= ${BUILD_DIR}/reports/tests-junit.xml
## Specify the location where the full JSON report shall be written to.
TEST_JSON_REPORT  ?=
## Specify the output directory for test coverage reports.
TEST_COVERAGE_DIR ?= ${BUILD_DIR}/cover
## Directory where all runtime test data is being created.
TEST_TMP_DIR      ?=
## Directory where Gitaly should write logs to during test execution.
TEST_LOG_DIR	  ?=
TEST_REPO_DIR     := ${BUILD_DIR}/testrepos
BENCHMARK_REPO    := ${TEST_REPO_DIR}/benchmark.git
## Options to pass to the script which builds the Gitaly gem
BUILD_GEM_OPTIONS ?=
## Options to override the name of Gitaly gem
BUILD_GEM_NAME ?= gitaly

# All executables provided by Gitaly.
GITALY_EXECUTABLES           = $(addprefix ${BUILD_DIR}/bin/,$(notdir $(shell find ${SOURCE_DIR}/cmd -mindepth 1 -maxdepth 1 -type d -print)))
# All executables packed inside the Gitaly binary.
GITALY_PACKED_EXECUTABLES    = $(filter %gitaly-hooks %gitaly-gpg %gitaly-ssh %gitaly-lfs-smudge, ${GITALY_EXECUTABLES})
# All executables that should be installed.
GITALY_INSTALLED_EXECUTABLES = $(filter-out ${GITALY_PACKED_EXECUTABLES}, ${GITALY_EXECUTABLES})
# Find all Go source files.
find_go_sources              = $(shell find ${SOURCE_DIR} -type d \( -path "${SOURCE_DIR}/_*" -o -path "${SOURCE_DIR}/proto" \) -prune -o -type f -name '*.go' -print | sort -u)

# run_go_tests will execute Go tests with all required parameters. Its
# behaviour can be modified via the following variables:
#
# TEST_OPTIONS: any additional options
# TEST_PACKAGES: packages which shall be tested
# TEST_LOG_DIR: specify the output log dir. By default, all logs will be discarded
run_go_tests = PATH='${SOURCE_DIR}/internal/testhelper/testdata/home/bin:${PATH}' \
    TEST_TMP_DIR='${TEST_TMP_DIR}' \
    TEST_LOG_DIR='${TEST_LOG_DIR}' \
    ${GOTESTSUM} --format ${TEST_FORMAT} --junitfile '${TEST_JUNIT_REPORT}' --jsonfile '${TEST_JSON_REPORT}' -- -ldflags '${GO_LDFLAGS}' -tags '${SERVER_BUILD_TAGS}' ${TEST_OPTIONS} ${TEST_PACKAGES}

## Test options passed to `dlv test`.
DEBUG_OPTIONS      ?= $(patsubst -%,-test.%,${TEST_OPTIONS})

# debug_go_tests will execute Go tests from a single package in the delve debugger.
# Its behaviour can be modified via the following variable:
#
# DEBUG_OPTIONS: any additional options, will default to TEST_OPTIONS if not set.
debug_go_tests = PATH='${SOURCE_DIR}/internal/testhelper/testdata/home/bin:${PATH}' \
    TEST_TMP_DIR='${TEST_TMP_DIR}' \
    ${DELVE} test --build-flags="-ldflags '${GO_LDFLAGS}' -tags '${SERVER_BUILD_TAGS}'" ${TEST_PACKAGES} -- ${DEBUG_OPTIONS}

unexport GOROOT
## GOCACHE_MAX_SIZE_KB is the maximum size of Go's build cache in kilobytes before it is cleaned up.
GOCACHE_MAX_SIZE_KB              ?= 5000000
export GOCACHE                   ?= ${BUILD_DIR}/cache
export GOPROXY                   ?= https://proxy.golang.org|https://proxy.golang.org
export PATH                      := ${BUILD_DIR}/bin:${PATH}

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
	${Q}echo "These are common variables which can be overridden in config.mak"
	${Q}echo "or by passing them to make directly as environment variables:"
	${Q}echo ""

	@ # Match all variables which have preceding `## ` comments and which are assigned via `?=`.
	${Q}awk '/^[[:space:]]*## / { sub(/^[[:space:]]*##/,"",$$0) ; desc = desc $$0 ; next } \
		 /^[[:space:]]*[[:alpha:]][[:alnum:]_-]+[[:space:]]*\?=/ && desc { print "  "$$1 ":" desc } \
		 { desc = "" }' $(MAKEFILE_LIST) | sort | column -s: -t

.PHONY: build
## Build Go binaries.
build: ${GITALY_INSTALLED_EXECUTABLES}

.PHONY: install
## Install Gitaly binaries. The target directory can be modified by setting PREFIX and DESTDIR.
install: build
	${Q}mkdir -p ${INSTALL_DEST_DIR}
	install ${GITALY_INSTALLED_EXECUTABLES} "${INSTALL_DEST_DIR}"

.PHONY: build-bundled-git
## Build bundled Git binaries.
build-bundled-git: build-bundled-git-v2.44 build-bundled-git-v2.45
build-bundled-git-v2.44: $(patsubst %,${BUILD_DIR}/bin/gitaly-%-v2.44,${GIT_EXECUTABLES})
build-bundled-git-v2.45: $(patsubst %,${BUILD_DIR}/bin/gitaly-%-v2.45,${GIT_EXECUTABLES})

.PHONY: install-bundled-git
## Install bundled Git binaries. The target directory can be modified by
## setting PREFIX and DESTDIR.
install-bundled-git: install-bundled-git-v2.44 install-bundled-git-v2.45
install-bundled-git-v2.44: $(patsubst %,${INSTALL_DEST_DIR}/gitaly-%-v2.44,${GIT_EXECUTABLES})
install-bundled-git-v2.45: $(patsubst %,${INSTALL_DEST_DIR}/gitaly-%-v2.45,${GIT_EXECUTABLES})

ifdef WITH_BUNDLED_GIT
build: build-bundled-git
prepare-tests: build-bundled-git
install: install-bundled-git

export GITALY_TESTING_BUNDLED_GIT_PATH ?= ${BUILD_DIR}/bin
else
prepare-tests: ${DEPENDENCY_DIR}/git-distribution/git

export GITALY_TESTING_GIT_BINARY ?= ${DEPENDENCY_DIR}/git-distribution/bin-wrappers/git
endif

## Enable testing with the SHA256 object format.
TEST_WITH_SHA256 ?=
ifdef TEST_WITH_SHA256
export GITALY_TEST_WITH_SHA256 = YesPlease
endif

## Enable generating test coverage.
TEST_WITH_COVERAGE ?=
ifdef TEST_WITH_COVERAGE
override TEST_OPTIONS := ${TEST_OPTIONS} -coverprofile "${TEST_COVERAGE_DIR}/all.merged"
prepare-tests: ${GOCOVER_COBERTURA} ${TEST_COVERAGE_DIR}

.PHONY: ${TEST_COVERAGE_DIR}
${TEST_COVERAGE_DIR}:
	${Q}rm -rf "${TEST_COVERAGE_DIR}"
	${Q}mkdir -p "${TEST_COVERAGE_DIR}"

# sed is used below to convert file paths to repository root relative paths.
# See https://gitlab.com/gitlab-org/gitlab/-/issues/217664
run_go_tests += \
	&& go tool cover -html  "${TEST_COVERAGE_DIR}/all.merged" -o "${TEST_COVERAGE_DIR}/all.html" \
	&& ${GOCOVER_COBERTURA} <"${TEST_COVERAGE_DIR}/all.merged" | \
	sed 's;filename=\"$(shell go list -m)/;filename=\";g' >"${TEST_COVERAGE_DIR}/cobertura.xml"
endif

.PHONY: prepare-tests
prepare-tests: ${GOTESTSUM} ${GITALY_PACKED_EXECUTABLES} ${GIT_FILTER_REPO}
	${Q}mkdir -p "$(dir ${TEST_JUNIT_REPORT})"

.PHONY: prepare-debug
prepare-debug: ${DELVE}

.PHONY: test
## Run Go tests.
test: test-go test-gitaly-linters

.PHONY: test-gitaly-linters
## Test Go tests in tools/golangci-lint/gitaly folder
## That folder has its own go.mod file. Hence tests must run the context of that folder.
test-gitaly-linters: TEST_PACKAGES := .
test-gitaly-linters: ${GOTESTSUM}
	${Q}cd ${SOURCE_DIR}/tools/golangci-lint/gitaly && $(call run_go_tests)

.PHONY: test-go
## Run Go tests.
test-go: prepare-tests ${GOCOVER_COBERTURA}
	${Q}$(call run_go_tests)

.PHONY: debug-test-go
## Run Go tests in delve debugger.
debug-test-go: prepare-tests prepare-debug
	${Q}$(call debug_go_tests)

.PHONY: test
## Run Go benchmarks.
bench: override TEST_FORMAT := standard-verbose
bench: override TEST_OPTIONS := -bench=. -run=^$ ${TEST_OPTIONS}
bench: ${BENCHMARK_REPO} prepare-tests
	${Q}$(call run_go_tests)

.PHONY: test-with-reftable
## Run Go tests with git's reftable backend.
## Since the reftable code isn't tagged in Git yet, to run it locally
## we have to specify the git version too:
## 'GIT_DEFAULT_REF_FORMAT=reftable OVERRIDE_GIT_VERSION="v99.99.99" GIT_VERSION="master" make test-go'
test-with-reftable: export GIT_DEFAULT_REF_FORMAT = reftable
test-with-reftable: test-go

.PHONY: test-with-praefect
## Run Go tests with Praefect.
test-with-praefect: export GITALY_TEST_WITH_PRAEFECT = YesPlease
test-with-praefect: test-go

.PHONY: test-wal
## Run Go tests with write-ahead logging enabled.
test-wal: export GITALY_TEST_WAL = YesPlease
test-wal: test-go

.PHONY: test-with-praefect-wal
## Run Go tests with write-ahead logging and Praefect enabled.
test-with-praefect-wal: export GITALY_TEST_WAL = YesPlease
test-with-praefect-wal: test-with-praefect

.PHONY: race-go
## Run Go tests with race detection enabled.
race-go: override TEST_OPTIONS := ${TEST_OPTIONS} -race
race-go: test-go

## Running the verify steps in parallel make it difficult to read the output
## when there are failures.
.NOTPARALLEL: verify

.PHONY: verify
## Verify that various files conform to our expectations.
verify: check-mod-tidy notice-up-to-date check-proto lint

.PHONY: check-mod-tidy
check-mod-tidy:
	${Q}${GIT} diff --quiet --exit-code go.mod go.sum || (echo "error: uncommitted changes in go.mod or go.sum" && exit 1)
	${Q}go mod tidy
	${Q}${GIT} diff --quiet --exit-code go.mod go.sum || (echo "error: uncommitted changes in go.mod or go.sum" && exit 1)

${TOOLS_DIR}/gitaly-linters.so: ${SOURCE_DIR}/tools/golangci-lint/go.sum $(wildcard ${SOURCE_DIR}/tools/golangci-lint/gitaly/*.go)
	${Q}go build -buildmode=plugin -o '$@' -modfile ${SOURCE_DIR}/tools/golangci-lint/go.mod $(filter-out $<,$^)

.PHONY: lint
## Run Go linter.
lint: ${GOLANGCI_LINT} ${GITALY_PACKED_EXECUTABLES} ${TOOLS_DIR}/gitaly-linters.so lint-gitaly-linters
	${Q}${GOLANGCI_LINT} run --build-tags "${SERVER_BUILD_TAGS}" --out-format tab --config ${GOLANGCI_LINT_CONFIG} ${GOLANGCI_LINT_OPTIONS}

.PHONY: lint-fix
## Run Go linter and write back fixes to the files (not supported by all linters).
lint-fix: ${GOLANGCI_LINT} ${GITALY_PACKED_EXECUTABLES} ${TOOLS_DIR}/gitaly-linters.so
	${Q}${GOLANGCI_LINT} run --fix --build-tags "${SERVER_BUILD_TAGS}" --out-format tab --config ${GOLANGCI_LINT_CONFIG} ${GOLANGCI_LINT_OPTIONS}

.PHONY: lint-docs
## Run markdownlint-cli2-config to lint the documentation and lychee to check for broken links.
lint-docs:
	${Q}markdownlint-cli2 README.md REVIEWING.md STYLE.md doc/**/*.md doc/*.md
	${Q}lychee --version
	${Q}lychee --offline --include-fragments README.md REVIEWING.md STYLE.md doc/**/*.md doc/*.md

.PHONY: lint-docs-fix
## Run markdownlint-cli2-config to lint and fix the documentation.
lint-docs-fix:
	${Q}markdownlint-cli2 README.md REVIEWING.md STYLE.md doc/**.md --fix

.PHONY: lint-gitaly-linters
## Test Go tests in tools/golangci-lint/gitaly folder
## That folder has its own go.mod file. Hence linter must run the context of that folder.
lint-gitaly-linters: ${GOLANGCI_LINT} ${TOOLS_DIR}/gitaly-linters.so
	${Q}cd ${SOURCE_DIR}/tools/golangci-lint/gitaly && ${GOLANGCI_LINT} run --out-format tab --config ${GOLANGCI_LINT_CONFIG} .

.PHONY: format
## Run Go formatter and adjust imports.
format: ${GOIMPORTS} ${GOFUMPT}
	${Q}${GOIMPORTS} -w -l $(call find_go_sources)
	${Q}${GOFUMPT} -w $(call find_go_sources)

.PHONY: notice-up-to-date
notice-up-to-date: ${BUILD_DIR}/NOTICE
	${Q}${GIT} diff --exit-code ${BUILD_DIR}/NOTICE ${SOURCE_DIR}/NOTICE || (echo >&2 "NOTICE requires update: 'make notice'" && exit 1)

.PHONY: notice
## Regenerate the NOTICE file.
notice: ${SOURCE_DIR}/NOTICE

.PHONY: clean
## Clean up the build artifacts.
clean:
	rm -rf ${BUILD_DIR}

.PHONY: proto
## Regenerate protobuf definitions.
proto: SHARED_PROTOC_OPTS = --plugin=${PROTOC_GEN_GO} --plugin=${PROTOC_GEN_GO_GRPC} --plugin=${PROTOC_GEN_GITALY_PROTOLIST} --go_opt=paths=source_relative --go-grpc_opt=paths=source_relative
proto: ${PROTOC} ${PROTOC_GEN_GO} ${PROTOC_GEN_GO_GRPC} ${PROTOC_GEN_GITALY_PROTOLIST}
	${Q}rm -rf ${PROTO_DEST_DIR} && mkdir -p ${PROTO_DEST_DIR}/gitalypb
	${Q}${PROTOC} ${SHARED_PROTOC_OPTS} -I ${SOURCE_DIR}/proto -I ${PROTOC_INSTALL_DIR}/include --go_out=${PROTO_DEST_DIR}/gitalypb --gitaly-protolist_out=proto_dir=${SOURCE_DIR}/proto,gitalypb_dir=${PROTO_DEST_DIR}/gitalypb:${SOURCE_DIR} --go-grpc_out=${PROTO_DEST_DIR}/gitalypb ${SOURCE_DIR}/proto/*.proto ${SOURCE_DIR}/proto/testproto/*.proto

.PHONY: check-proto
check-proto: no-proto-changes lint-proto

.PHONY: lint-proto
lint-proto: ${PROTOC} ${PROTOLINT} ${PROTOC_GEN_GITALY_LINT}
	${Q}${PROTOC} -I ${SOURCE_DIR}/proto -I ${PROTOC_INSTALL_DIR}/include --plugin=${PROTOC_GEN_GITALY_LINT} --gitaly-lint_out=${SOURCE_DIR} ${SOURCE_DIR}/proto/*.proto
	${Q}${PROTOLINT} lint -config_dir_path=${SOURCE_DIR}/proto ${SOURCE_DIR}/proto/*.proto

.PHONY: build-proto-gem
## Build the Ruby Gem that contains Gitaly's Protobuf definitions.
build-proto-gem:
	${Q}rm -rf "${BUILD_DIR}/${BUILD_GEM_NAME}.gem" && mkdir -p ${BUILD_DIR}
	${Q}rm -rf "${BUILD_DIR}/${BUILD_GEM_NAME}-gem" && mkdir -p ${BUILD_DIR}/${BUILD_GEM_NAME}-gem
	${Q}cd "${SOURCE_DIR}"/tools/protogem && bundle install
	${Q}"${SOURCE_DIR}"/tools/protogem/build-proto-gem -o "${BUILD_DIR}/${BUILD_GEM_NAME}.gem" --name ${BUILD_GEM_NAME} --working-dir ${BUILD_DIR}/${BUILD_GEM_NAME}-gem ${BUILD_GEM_OPTIONS}

.PHONY: publish-proto-gem
## Build and publish the Ruby Gem that contains Gitaly's Protobuf definitions.
publish-proto-gem: build-proto-gem
	${Q}gem push "${BUILD_DIR}/gitaly.gem"

.PHONY: build-proto-docs
## Build HTML documentation for Gitaly Protobuf definitions.
build-proto-docs: ${PROTOC} ${PROTOC_GEN_DOC}
	${Q}rm -rf ${BUILD_DIR}/proto-docs && mkdir -p ${BUILD_DIR}/proto-docs
	${Q}${PROTOC} -I ${SOURCE_DIR}/proto -I ${PROTOC_INSTALL_DIR}/include --doc_out=${BUILD_DIR}/proto-docs --doc_opt=html,index.html --plugin=protoc-gen-doc=${PROTOC_GEN_DOC} ${SOURCE_DIR}/proto/*.proto

.PHONY: govulncheck
## pipefail is set in the SHELL config, but we don't care about govulncheck's exit code here.
govulncheck: ${GOVULNCHECK}
	${Q}(${GOVULNCHECK} ./... || true) | go run ${SOURCE_DIR}/tools/govulncheck-filter/main.go

.PHONY: no-changes
no-changes:
	${Q}${GIT} diff --exit-code

.PHONY: no-proto-changes
no-proto-changes: PROTO_DEST_DIR := ${BUILD_DIR}/proto-changes
no-proto-changes: proto | ${BUILD_DIR}
	${Q}${GIT} diff --no-index --exit-code -- "${SOURCE_DIR}/proto/go" "${PROTO_DEST_DIR}"

.PHONY: dump-database-schema
## Dump the clean database schema of Praefect into a file.
dump-database-schema: build
	${Q}"${SOURCE_DIR}"/_support/generate-praefect-schema >"${SOURCE_DIR}"/_support/praefect-schema.sql

.PHONY: upgrade-module
upgrade-module:
	${Q}go run ${SOURCE_DIR}/tools/module-updater/main.go -dir . -from=${FROM_MODULE} -to=${TO_MODULE}
	${Q}${MAKE} proto

.PHONY: git
# This target is deprecated and will eventually be removed.
git: install-git

.PHONY: build-git
## Build Git distribution.
build-git: ${DEPENDENCY_DIR}/git-distribution/git

.PHONY: install-git
## Install Git distribution.
install-git: build-git
	${Q}env -u PROFILE -u MAKEFLAGS -u GIT_VERSION ${MAKE} -C "${DEPENDENCY_DIR}/git-distribution" -j$(shell nproc) prefix=${GIT_PREFIX} ${GIT_BUILD_OPTIONS} install

${SOURCE_DIR}/NOTICE: ${BUILD_DIR}/NOTICE
	${Q}mv $< $@

${BUILD_DIR}/NOTICE: ${GO_LICENSES} ${GITALY_PACKED_EXECUTABLES}
	${Q}rm -rf ${BUILD_DIR}/licenses
	${Q}GOOS=linux GOFLAGS="-tags=${SERVER_BUILD_TAGS}" ${GO_LICENSES} save ${SOURCE_DIR}/... --save_path=${BUILD_DIR}/licenses
	${Q}go run ${SOURCE_DIR}/tools/noticegen/noticegen.go -source ${BUILD_DIR}/licenses -template ${SOURCE_DIR}/tools/noticegen/notice.template > ${BUILD_DIR}/NOTICE

${BUILD_DIR}:
	${Q}mkdir -p ${BUILD_DIR}
${BUILD_DIR}/bin: | ${BUILD_DIR}
	${Q}mkdir -p ${BUILD_DIR}/bin
${TOOLS_DIR}: | ${BUILD_DIR}
	${Q}mkdir -p ${TOOLS_DIR}
${DEPENDENCY_DIR}: | ${BUILD_DIR}
	${Q}mkdir -p ${DEPENDENCY_DIR}

# This target builds a full Git distribution.
${DEPENDENCY_DIR}/git-distribution/git: ${DEPENDENCY_DIR}/git-distribution/Makefile
	${Q}env -u PROFILE -u MAKEFLAGS -u GIT_VERSION ${MAKE} -C "$(<D)" -j$(shell nproc) prefix=${GIT_PREFIX} ${GIT_BUILD_OPTIONS}
	${Q}touch $@

${BUILD_DIR}/bin/gitaly-%-v2.44: override GIT_VERSION = ${GIT_VERSION_2_44}
${BUILD_DIR}/bin/gitaly-%-v2.44: ${DEPENDENCY_DIR}/git-v2.44/% | ${BUILD_DIR}/bin
	${Q}install $< $@

${BUILD_DIR}/bin/gitaly-%-v2.45: override GIT_VERSION = ${GIT_VERSION_2_45}
${BUILD_DIR}/bin/gitaly-%-v2.45: ${DEPENDENCY_DIR}/git-v2.45/% | ${BUILD_DIR}/bin
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

# clear-go-build-cache-if-needed cleans the Go build cache if it exceeds the maximum size as
# configured in GOCACHE_MAX_SIZE_KB.
.PHONY: clear-go-build-cache-if-needed
clear-go-build-cache-if-needed:
	${Q}if [ -d ${GOCACHE} ] && [ $$(du -sk ${GOCACHE} | cut -f 1) -gt ${GOCACHE_MAX_SIZE_KB} ]; then go clean --cache; fi

${BUILD_DIR}/intermediate/gitaly:            GO_BUILD_TAGS = ${SERVER_BUILD_TAGS}
${BUILD_DIR}/intermediate/gitaly:            ${GITALY_PACKED_EXECUTABLES}
${BUILD_DIR}/intermediate/praefect:          GO_BUILD_TAGS = ${SERVER_BUILD_TAGS}
${BUILD_DIR}/intermediate/%:                 clear-go-build-cache-if-needed .FORCE
	@ # We're building intermediate binaries first which contain a fixed build ID
	@ # of "TEMP_GITALY_BUILD_ID". In the final binary we replace this build ID with
	@ # the computed build ID for this binary.
	@ # We cd to SOURCE_DIR to avoid corner cases where workdir may be a symlink
	${Q}cd ${SOURCE_DIR} && go build -o "$@" -ldflags '-B 0x${TEMPORARY_BUILD_ID} ${GO_LDFLAGS}' -tags "${GO_BUILD_TAGS}" $(addprefix ${SOURCE_DIR}/cmd/,$(@F))

# This is a build hack to avoid excessive rebuilding of targets. Instead of
# depending on the Makefile, we start to depend on tool versions as defined in
# the Makefile. Like this, we only rebuild if the tool versions actually
# change. The dependency on the phony target is required to always rebuild
# these targets.
.PHONY: dependency-version
${DEPENDENCY_DIR}/git-%.version: dependency-version | ${DEPENDENCY_DIR}
	${Q}[ x"$$(cat "$@" 2>/dev/null)" = x"${GIT_VERSION} ${GIT_BUILD_OPTIONS}" ] || >$@ echo -n "${GIT_VERSION} ${GIT_BUILD_OPTIONS}"
${DEPENDENCY_DIR}/protoc.version: dependency-version | ${DEPENDENCY_DIR}
	${Q}[ x"$$(cat "$@" 2>/dev/null)" = x"${PROTOC_VERSION} ${PROTOC_BUILD_OPTIONS}" ] || >$@ echo -n "${PROTOC_VERSION} ${PROTOC_BUILD_OPTIONS}"
${DEPENDENCY_DIR}/git-filter-repo.version: dependency-version | ${DEPENDENCY_DIR}
	${Q}[ x"$$(cat "$@" 2>/dev/null)" = x"${GIT_FILTER_REPO_VERSION}" ] || >$@ echo -n "${GIT_FILTER_REPO_VERSION}"
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
ifeq ($(OVERRIDE_GIT_VERSION),)
	${Q}rm -f "${@D}"/version
else
	@ # We're writing the version into the "version" file in Git's own source
	@ # directory. If it exists, Git's Makefile will pick it up and use it as
	@ # the version instead of auto-detecting via git-describe(1).
	${Q}echo ${OVERRIDE_GIT_VERSION} >"${@D}"/version
endif
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

${PROTOC_GEN_GITALY_LINT}: proto | ${TOOLS_DIR}
	${Q}go build -o $@ ${SOURCE_DIR}/tools/protoc-gen-gitaly-lint

${PROTOC_GEN_GITALY_PROTOLIST}: | ${TOOLS_DIR}
	${Q}go build -o $@ ${SOURCE_DIR}/tools/protoc-gen-gitaly-protolist

${GIT_FILTER_REPO}: ${DEPENDENCY_DIR}/git-filter-repo.version | ${BUILD_DIR}/bin
	${Q}${GIT} -c init.defaultBranch=master init ${GIT_QUIET} "${GIT_FILTER_REPO_SOURCE_DIR}"
	${Q}${GIT} -C "${GIT_FILTER_REPO_SOURCE_DIR}" config remote.origin.url ${GIT_FILTER_REPO_REPO_URL}
	${Q}${GIT} -C "${GIT_FILTER_REPO_SOURCE_DIR}" config remote.origin.tagOpt --no-tags
	${Q}${GIT} -C "${GIT_FILTER_REPO_SOURCE_DIR}" fetch --depth 1 ${GIT_QUIET} origin ${GIT_FILTER_REPO_VERSION}
	${Q}${GIT} -C "${GIT_FILTER_REPO_SOURCE_DIR}" reset --hard
	${Q}${GIT} -C "${GIT_FILTER_REPO_SOURCE_DIR}" checkout ${GIT_QUIET} --detach FETCH_HEAD
	${Q}cp "${GIT_FILTER_REPO_SOURCE_DIR}/git-filter-repo" ${GIT_FILTER_REPO}

# External tools
${TOOLS_DIR}/%: ${SOURCE_DIR}/tools/%/tool.go ${SOURCE_DIR}/tools/%/go.mod ${SOURCE_DIR}/tools/%/go.sum | ${TOOLS_DIR}
	${Q}GOBIN=${TOOLS_DIR} go install -modfile ${SOURCE_DIR}/tools/$*/go.mod ${TOOL_PACKAGE}

${GOCOVER_COBERTURA}: TOOL_PACKAGE = github.com/t-yuki/gocover-cobertura
${GOFUMPT}:           TOOL_PACKAGE = mvdan.cc/gofumpt
${GOIMPORTS}:         TOOL_PACKAGE = golang.org/x/tools/cmd/goimports
${GOLANGCI_LINT}:     TOOL_PACKAGE = github.com/golangci/golangci-lint/cmd/golangci-lint
${PROTOLINT}:         TOOL_PACKAGE = github.com/yoheimuta/protolint/cmd/protolint
${GOTESTSUM}:         TOOL_PACKAGE = gotest.tools/gotestsum
${GO_LICENSES}:       TOOL_PACKAGE = github.com/google/go-licenses
${PROTOC_GEN_GO}:     TOOL_PACKAGE = google.golang.org/protobuf/cmd/protoc-gen-go
${PROTOC_GEN_GO_GRPC}:TOOL_PACKAGE = google.golang.org/grpc/cmd/protoc-gen-go-grpc
${PROTOC_GEN_DOC}:    TOOL_PACKAGE = github.com/pseudomuto/protoc-gen-doc/cmd/protoc-gen-doc
${DELVE}:             TOOL_PACKAGE = github.com/go-delve/delve/cmd/dlv
${GOVULNCHECK}:       TOOL_PACKAGE = golang.org/x/vuln/cmd/govulncheck

${BENCHMARK_REPO}:
	${GIT} clone --mirror ${GIT_QUIET} https://gitlab.com/gitlab-org/gitlab.git $@
