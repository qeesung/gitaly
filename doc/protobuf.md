# Protobuf specifications and client libraries for Gitaly

Gitaly is part of GitLab. It is a [server
application](https://gitlab.com/gitlab-org/gitaly) that uses its own
gRPC protocol to communicate with its clients. This repository
contains the protocol definition and automatically generated wrapper
code for Go and Ruby.

The `.proto` files define the remote procedure calls for interacting
with Gitaly. We keep auto-generated client libraries for Ruby and Go
in their respective subdirectories. The list of RPCs can be
[found here](https://gitlab-org.gitlab.io/gitaly-proto/).

Run `make proto` from the root of the repository to regenerate the client
libraries after updating .proto files.

## Gitaly RPC documentation

For documentation generated from the protobuf definitions (in `proto/` directory),
see [Gitaly RPC documentation](https://gitlab-org.gitlab.io/gitaly/).

See
[`developers.google.com`](https://developers.google.com/protocol-buffers/docs/proto3)
for documentation of the 'proto3' Protocol buffer specification
language.

## gRPC/Protobuf concepts

The core Protobuf concepts we use are rpc, service and message. We use
these to define the Gitaly **protocol**.

- **rpc** a function that can be called from the client and that gets
  executed on the server. Belongs to a service. Can have one of four
  request/response signatures: message/message (example: get metadata for
  commit xxx), message/stream (example: get contents of blob xxx),
  stream/message (example: create new blob with contents xxx),
  stream/stream (example: Git SSH session).
- **service** a logical group of RPC's.
- **message** like a JSON object except it has pre-defined types.
- **stream** an unbounded sequence of messages. In the Ruby clients
  this looks like an Enumerator.

gRPC provides an implementation framework based on these Protobuf concepts.

- A gRPC **server** implements one or more services behind a network
  listener. Example: the Gitaly server application.
- The gRPC toolchain automatically generates **client libraries** that
  handle serialization and connection management. Example: the Go
  client package and Ruby gem in this repository.
- gRPC **clients** use the client libraries to make remote procedure
  calls. These clients must decide what network address to reach their
  gRPC servers on and handle connection reuse: it is possible to
  spread different gRPC services over multiple connections to the same
  gRPC server.
- Officially a gRPC connection is called a **channel**. In the Go gRPC
  library these channels are called **client connections** because
  'channel' is already a concept in Go itself. In Ruby a gRPC channel
  is an instance of GRPC::Core::Channel. We use the word 'connection'
  in this document. The underlying transport of gRPC, HTTP/2, allows
  multiple remote procedure calls to happen at the same time on a
  single connection to a gRPC server. In principle, a multi-threaded
  gRPC client needs only one connection to a gRPC server.

## Gitaly RPC Server Architecture

Gitaly consists of two different server applications which implement services:

- Gitaly hosts all the logic required to access and modify Git repositories.
  This is the place where actual repositories reside and where the Git commands
  get executed.

- Praefect is a transparent proxy that routes requests to one or more Gitaly
  nodes. This server allows for load-balancing and high availability by keeping
  multiple Gitaly nodes up-to-date with the same data.

Gitaly clients either interact with Praefect or with a single Gitaly server. For
most of the part the client does not need to know which of both types of servers
it is currently interacting with: Praefect transparently proxies requests to
Gitaly servers so that it behaves the same as a standalone Gitaly server.

Servers can be sharded for larger installations so that only a subset of data is
stored on each of the Gitaly servers.

## Design

### RPC definitions

Each RPC `FooBar` has its own `FooBarRequest` and `FooBarResponse` message
types. Try to keep the structure of these messages as flat as possible. Only add
abstractions when they have a practical benefit.

We never make backwards incompatible changes to an RPC that is already
implemented on either the client side or server side. Instead we just create a
new RPC call and start a deprecation procedure (see below) for the old one.

### Comments

Services, RPCs, messages and their fields declared in `.proto` files must have
comments. This documentation must be sufficient to let potential callers figure
out why this RPC exists and what the behaviour of an RPC is without looking up
its implementation. Special error cases should be documented.

### Errors

Gitaly uses [error codes](https://pkg.go.dev/google.golang.org/grpc/codes) to
indicate basic error classes. In case error codes are not sufficient for clients
to make specific error cases actionable, Gitaly uses the [rich error
model](https://www.grpc.io/docs/guides/error/#richer-error-model) provided by
gRPC. With this error model, Gitaly can embed Protobuf messages into returned
errors and thus provide exact information about error conditions to the client.
In case the RPC needs to use the rich error model, it should have its own
`FooBarError` message type.

RPCs must return an error if the action failed. It is disallowed to return
specific error cases via the RPC's normal response. This is required so that
Praefect can correctly handle any such errors.

### RPC concepts

RPCs should not be focussed on a single usecase only, but instead they should be
implemented with the underlying Git concept in mind. If they directly map to the
way Git handles specific data instead of directly mapping to the usecase at
hand, then chances are high that the RPC will be reusable for other, yet-unknown
usecases.

Common concepts that can be considered:

- Accept revisions as documented in gitrevisions(5) instead of object IDs or
  references. If possible, accept an array of revisions instead of a single
  revision only so that callers can easily specify revision ranges without
  requiring a separate RPC. Furthermore, accept pseudo-revisions like `--not`
  and `--all`.
- Accept fully-qualified references instead of branch names. This avoids issues
  with ambiguity and makes it possible to use RPCs for references which are not
  branches.

RPCs should not implement business-specific logic and policy, but should only
provide the means to handle data in the problem-domain of Gitaly and/or
Praefect. The goal of this is to ensure that the Gitaly project creates an
interface to manage Git data, but does not make business decisions around how to
manage the data.

For example, Gitaly can provide a robust and efficient set of APIs to move Git
repositories between storage solutions, but it would be up to the calling
application to decide when such moves should occur.

### RPC naming conventions

Gitaly has RPCs that are resource based, for example when querying for a commit.
Another class of RPCs are operations, where the result might be empty or one of
the RPC error codes but the fact that the operation took place is of importance.

For all RPCs, start the name with a verb, followed by an entity, and if required
followed by a further specification. For example:

- ListCommits
- RepackRepositoryIncremental
- CreateRepositoryFromBundle

For resource RPCs the verbs in use are limited to:

- Get
- List
- Is
- Create
- Update
- Delete

Get and List as verbs denote these operations have no side effects. These verbs
differ in terms of the expected number of results the query yields. Get queries
are limited to one result, and are expected to return one result to the client.
List queries have zero or more results, and generally will create a gRPC stream
for their results.

When the `Is` verb is used, this RPC is expected to return a boolean, or an
error. For example: `IsRepositoryEmpty`.

When an operation-based RPC is defined, the verb should map to the first verb in
the Git command it represents, e.g. `FetchRemote`.

Note that large parts of the current Gitaly RPC interface do not abide fully to
these conventions. Newly defined RPCs should, though, so eventually the
interface converges to a common standard.

### Common field names and types

As a general principle, remember that Git does not enforce encodings on most
data inside repositories, so we can rarely assume data to be a Protobuf "string"
(which implies UTF-8).

1. `bytes revision`: for fields that accept any of branch names / tag
   names / commit ID's. Uses `bytes` to be encoding agnostic.
1. `string commit_id`: for fields that accept a commit ID.
1. `bytes ref`: for fields that accept a refname.
1. `bytes path`: for paths inside Git repositories, i.e., inside Git
   `tree` objects.
1. `string relative_path`: for paths on disk on a Gitaly server,
   created by "us" (GitLab the application) instead of the user, we
   want to use UTF-8, or better, ASCII.

### Stream patterns

Protobuf supports streaming RPCs which allow for multiple request or response
messages to be sent in a single RPC call. We use these whenever it is expected
that an RPC may be invoked with lots of input parameters or when it may generate
a lot of data. This is required by limitations in the gRPC framework where
messages should not typically be larger than 1MB.

#### Stream response of many small items

```protobuf
rpc FooBar(FooBarRequest) returns (stream FooBarResponse);

message FooBarResponse {
  message Item {
    // ...
  }
  repeated Item items = 1;
}
```

A typical example of an "Item" would be a commit. To avoid the penalty of
network IO for each Item we return, we batch them together. You can think of
this as a kind of buffered IO at the level of the Item messages. In Go, to ease
the bookkeeping you can use
[`gitlab.com/gitlab-org/gitaly/internal/helper/chunker`](https://pkg.go.dev/gitlab.com/gitlab-org/gitaly/internal/helper/chunker).

#### Single large item split over multiple messages

```protobuf
rpc FooBar(FooBarRequest) returns (stream FooBarResponse);

message FooBarResponse {
  message Header {
    // ...
  }

  oneof payload {
    Header header = 1;
    bytes data = 2;
  }
}
```

A typical example of a large item would be the contents of a Git blob. The
header might contain the blob OID and the blob size. Only the first message in
the response stream has `header` set, all others have `data` but no `header`.

In the particular case where you're sending back raw binary data from Go, you
can use
[`gitlab.com/gitlab-org/gitaly/streamio`](https://pkg.go.dev/gitlab.com/gitlab-org/gitaly/streamio)
to turn your gRPC response stream into an `io.Writer`.

> Note that a number of existing RPC's do not use this pattern exactly;
> they don't use `oneof`. In practice this creates ambiguity (does the
> first message contain non-empty `data`?) and encourages complex
> optimization in the server implementation (trying to squeeze data into
> the first response message). Using `oneof` avoids this ambiguity.

#### Many large items split over multiple messages

```protobuf
rpc FooBar(FooBarRequest) returns (stream FooBarResponse);

message FooBarResponse {
  message Header {
    // ...
  }

  oneof payload {
    Header header = 1;
    bytes data = 2;
  }
}
```

This looks the same as the "single large item" case above, except whenever a new
large item begins, we send a new message with a non-empty `header` field.

#### Footers

If the RPC requires it we can also send a footer using `oneof`. But by default,
we prefer headers.

### RPC Annotations

Gitaly Cluster needs to know about the nature of RPCs in order to decide how a
specific request needs to be routed:

- Accessors may be routed to any one Gitaly node which has an up-to-date
  repository to allow for load-balancing reads. These RPCs must not have any
  side effects.
- Mutators will be routed to all Gitaly nodes which have an up-to-date
  repository so that changes are performed on all nodes at once. Each node is
  expected to cast transactional votes so that the actual data that is written
  to disk is verified to be the same for all of them.
- Maintenance RPCs are not deemed mission critical. They are routed on a
  best-effort basis to all online nodes which have a specific repository.

To classify RPCs, each declaration must contain one of the following lines:

- `option (op_type).op = ACCESSOR;`
- `option (op_type).op = MUTATOR;`
- `option (op_type).op = MAINTENANCE;`

We use a custom `protoc` plugin to verify that all RPCs do in fact have such a
declaration. This plugin can be executed via `make lint-proto`.

Additionally, all mutator RPCs require additional annotations to clearly
indicate what is being modified:

- Server-scoped RPCs modify server-wide resources.
- Storage-scoped RPCs modify data in a specific storage.
- Repository-scoped RPCs modify data in a specific repository.

To declare the scope, mutators must contain one of the following lines:

- `option(op_type).scope = SERVER;`
- `option(op_type).scope = STORAGE;`: The associated request must have a field
  tagged with `[(storage)=true]` that indicates the storage's name.
- `option(op_type).scope = REPOSITORY;`: This is the default scoped and thus
  doesn't need to be explicitly declared. The associated request must have a
  field tagged with `[(target_repository)=true]` that indicates the repository's
  location.

The target repository represents the location or address of the repository being
modified by the operation. This is needed by Praefect (Gitaly Cluster) in order
to properly schedule replications to keep repository replicas up to date.

The target repository annotation marks where the target repository can be found
in the message. The annotation is added near `gitaly.Repository` field (e.g.
`Repository repository = 1 [(target_repository)=true];`). If annotated field
isn't `gitaly.Repository` type then it has to contain field annotated
`[(repository)=true]` with correct type. Having separate `repository` annotation
allows to have same field in child message annotated as both `target_repository`
and `additional_repository` depending on parent message.

The additional repository is annotated similarly to target repository but
annotation is named `additional_repository`.

See our examples of [valid](../proto/testproto/valid.proto) and
[invalid](../proto/testproto/invalid.proto) proto annotations.

### Transactions and Atomicity

With Gitaly Cluster, mutating RPCs will get routed to multiple Gitaly nodes at
once. Each node must then vote on the changes it intends to perform, and only if
quorum was reached on the change should it be persisted to disk. For this to
work correctly, all mutating RPCs need to follow a set of rules:

- Every mutator needs to vote at least twice on the data it is about to write: a
  first preparatory vote must happen before data is visible to the user so that
  data can be discarded in case nodes disagree without any impact on the
  repository itself. And a second committing vote must happen to let Praefect
  know that changes have indeed been committed to disk.
- In the general case, the vote should be computed from all data that is to be
  written.
- Changes should be atomic: either all changes are persisted to disk or none
  are.
- The number of transactional votes should be kept at a minimum and should not
  scale with the number of changes performed. Every vote incurs costs, which may
  become prohibitively expensive in case a vote is executed per change.
- Mutators must return an error in case anything unexpected happens. This error
  needs to be deterministic so that Praefect can assert that a failing RPC call
  has failed in the same way across nodes.

### Go Package

All Protobuf files hosted in the Gitaly project must have their Go package
declared. This is done via the `go_package` option:

`option go_package = "gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb";`

This allows other protobuf files to locate and import the Go generated stubs.

## Workflows

### Generating Protobuf sources

After you change or add a .proto file you need to re-generate the Go and Ruby
libraries before committing your change.

```shell
# Re-generate Go and Ruby libraries
make proto
```

### Verifying Protobuf definitions

Gitaly provides a `make lint-proto` target to verify that Protobuf definitions
conform to our coding style. Furthermore, Gitaly's CI verifies that sources
generated from the definitions are up-to-date by regenerating sources and then
running `no-proto-changes`.

### Deprecating an RPC call

See [PROCESS.md](PROCESS.md#rpc-deprecation-process).

## Releasing Protobuf definitions

See [PROCESS.md](PROCESS.md#publishing-the-ruby-gem).
