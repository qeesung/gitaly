# Request limiting in Gitaly

In the GitLab ecosystem, Gitaly is the service that is at the bottom of the
stack for Git data access. This means that when there is a surge of
requests to retrieve or change a piece of Git data, the I/O happens in Gitaly.
This can lead to Gitaly being overwhelmed due to system resource exhaustion
because all Git access goes through Gitaly.

If there is a surge of traffic beyond what Gitaly can handle, Gitaly should
be able to push back on the client calling. Gitaly shouldn't subserviently agree
to process more than it can handle.

We can turn several different knobs in Gitaly that put a limit on different kinds
of traffic patterns.

## Concurrency queue

Limit the number of concurrent RPCs that are in flight on each Gitaly node for each
repository per RPC using `[[concurrency]]` configuration:

```toml
[[concurrency]]
rpc = "/gitaly.SmartHTTPService/PostUploadPackWithSidechannel"
max_per_repo = 1
```

For example:

- One clone request comes in for repository "A" (a largish repository).
- While this RPC is executing, another request comes in for repository "A". Because
  `max_per_repo` is 1 in this case, the second request blocks until the first request
  is finished.

An in-memory queue of requests can build up in Gitaly that are waiting their turn. Because
this is a potential vector for a memory leak, two other values in the `[[concurrency]]`
configuration can prevent an unbounded in-memory queue of requests:

- `max_queue_wait` is the maximum amount of time a request can wait in the
  concurrency queue. When a request waits longer than this time, it returns
  an error to the client.
- `max_queue_size` is the maximum size the concurrency queue can grow for a given
  RPC. If a concurrency queue is at its maximum, subsequent requests
  return with an error.

For example:

```toml
[[concurrency]]
rpc = "/gitaly.SmartHTTPService/PostUploadPackWithSidechannel"
max_per_repo = 1
max_queue_wait = "1m"
max_queue_size = 5
```

## Rate limiting

To allow Gitaly to put back pressure on its clients, administrators can set a rate limit per
repository for each RPC:

```toml
[[rate_limiting]]
rpc =  "/gitaly.RepositoryService/RepackFull"
interval = "1m"
burst = 1
```

The rate limiter is implemented using the concept of a `token bucket`. A `token
bucket` has capacity `burst` and is refilled at an interval of `interval`. When a
request comes into Gitaly, a token is retrieved from the `token bucket` per
request. When the `token bucket` is empty, there are no more requests for that
RPC for a repository until the `token bucket` is refilled again. There is a `token bucket`
each RPC for each repository.

In the above configuration, the `token bucket` has a capacity of 1 and gets
refilled every minute. This means that Gitaly only accepts 1 `RepackFull`
request per repository each minute.

Requests that come in after the `token bucket` is full (and before it is 
replenished) are rejected with an error.

## Errors

With concurrency limiting and rate limiting, Gitaly responds with a structured
gRPC `gitalypb.LimitError` error with:

- A `Message` field that describes the error.
- A `BackoffDuration` field that provides the client with a time when it is safe to retry.
  If 0, it means it should never retry.

Gitaly clients (`gitlab-shell`, `workhorse`, Rails) must parse this error and
return sensible error messages to the end user. For example:

- Something trying to clone using HTTP or SSH.
- The GitLab application.
- Something calling the API.

## Metrics

Metrics are available that provide visibility into how these limits are being applied.
See the [GitLab Documentation](https://docs.gitlab.com/ee/administration/gitaly/#monitor-gitaly-and-gitaly-cluster) for details.
