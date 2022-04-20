# Request Limiting in Gitaly

## The Problem

In the GitLab ecosystem, Gitaly is the service that is at the bottom of the
stack as far as Git data access goes. This means that when there is a surge of
requests to retrieve or change a piece of Git data, the I/O happens in Gitaly.
This can lead to Gitaly being overloadeded due to system resource exhaustion,
since all roads lead to Gitaly.

## The Solution

If there is a surge of traffic beyond what Gitaly can handle, Gitaly should
be able to push back on the client calling it instead of subserviently agreeing
to bite off much more than it can chew.

There are several different knobs we can turn in Gitaly that put a limit on
different kinds of traffic patterns.

### Concurrency Queue

There is a way to limit the number of concurrent RPCs that are in flight per
Gitaly node/repository/RPC. This is done through the `[[concurrency]]`
configuration:

```toml
[[concurrency]]
rpc = "/gitaly.SmartHTTPService/PostUploadPackWithSidechannel"
max_per_repo = 1
```

Let's say that 1 clone request comes in for repo "A", and "A" is a largish
repository. While this RPC is executing, another request comes in for repo "A".
Since `max_per_repo` is 1 in this case, the second request will block until the
first request is finished. 

In this way, an in-memory queue of requests can build up in Gitaly that are
waiting their turn. Since this is a potential vector for a memory leak, there
are two other values in the `[[concurrency]]` config to prevent an unbounded
in-memory queue of requests.

```toml
[[concurrency]]
rpc = "/gitaly.SmartHTTPService/PostUploadPackWithSidechannel"
max_per_repo = 1
max_queue_wait = "1m"
max_queue_size = 5
```

`max_queue_wait` is the maximum amount of time a request can wait in the
concurrency queue. When a request waits longer than this time, it returns
an error to the client.

`max_queue_size` is the maximum size the concurrency queue can grow for a given
RPC for a repository. If a concurrency queue is at its maximum, subsequent requests
will return with an error.

### Rate Limiting

Another way to allow Gitaly to put backpressure on its clients is through rate
limiting. Admins can set a rate limit per repository or RPC:

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
refilled every minute. This means that Gitaly will only accept 1 `RepackFull`
request per repository each minute.

Requests that come in after the `token bucket` is full (and before it is 
replenished) are rejected with an error.

## Errors

With concurrency limiting as well as rate limiting, Gitaly will respond with a
structured gRPC error of the type `gitalypb.LimitError` with a `Message` field
that describes the error, and a `BackoffDuration` field that provides
the client with a time when it is safe to retry. If 0, it means it should never
retry.

Gitaly clients (gitlab-shell, workhorse, rails) all need to parse this error and
return sensible error messages to the end user. For example:

- Something trying to clone using HTTP or SSH.
- The GitLab application.
- Something calling the API.

## Metrics

There are metrics that provide visibility into how these limits are being
applied. See the [GitLab Documentation](https://docs.gitlab.com/ee/administration/gitaly/#monitor-gitaly-and-gitaly-cluster) for details.


