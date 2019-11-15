## Reason

For GitLab.com the [git access is slow](https://gitlab.com/gitlab-com/infrastructure/issues/351).

When looking at `Rugged::Repository.new` performance data we can see that our P99 spikes up to 30 wall seconds, while the CPU time keeps in the realm of the 15 milliseconds. Pointing at filesystem access as the culprit.

![rugged.new timings](doc/img/rugged-new-timings.png)

Our P99 access time to just create a `Rugged::Repository` object, which is loading and processing the git objects from disk, spikes over 30 seconds, making it basically unusable. We also saw that just walking through the branches of gitlab-ce requires 2.4 wall seconds.

We considered to move to metal to fix our problems with higher performance hardware. But our users are using GitLab in the cloud so it should work great there. And this way the increased performance will benefit every GitLab user.

Gitaly will make our situation better in a few steps:

1. One central place to monitor operations
1. Performance improvements doing less and caching more
1. Move the git operations from the app to the file/git server with git rpc (routing git access over JSON HTTP calls)
1. Use Git ketch to allow active-active (push to a local server), and distributed read operations (read from a secondary). This is far in the future, we might also use a distributed key value store instead. See the [active-active issue](https://gitlab.com/gitlab-org/gitlab-ee/issues/1381). Until we are active active we can just use persistent storage on the cloud to shard, this eliminates the need for redundancy.




## Scope

To maintain the focus of the project, the following subjects are out-of-scope for the moment:

1. Replication and high availability (including multi-master and active-active).


## References

- [GitHub diff pages](http://githubengineering.com/how-we-made-diff-pages-3x-faster/)
- [Bitbucket adaptive throttling](https://developer.atlassian.com/blog/2016/12/bitbucket-adaptive-throttling/)
- [Bitbucket caches](https://developer.atlassian.com/blog/2016/12/bitbucket-caches/)
- [GitHub Dgit (later Spokes)](http://githubengineering.com/introducing-dgit/)
- [GitHub Spokes (former Dgit)](http://githubengineering.com/building-resilience-in-spokes/)
- [Git Ketch](https://dev.eclipse.org/mhonarc/lists/jgit-dev/msg03073.html)
- [Lots of thinking in issue 2](https://gitlab.com/gitlab-org/gitaly/issues/2)
- [Git Pack Protocol Reference](https://github.com/git/git/blob/master/Documentation/technical/pack-protocol.txt)
- [Git Transfer Protocol internals](https://git-scm.com/book/en/v2/Git-Internals-Transfer-Protocols)
- [E3 Elastic Experiment Executor](https://bitbucket.org/atlassian/elastic-experiment-executor)

## Decisions

All design decisions should be added here.

1. Why are we considering to use Git Ketch? It is open source, uses the git protocol itself, is made by experts in distributed systems (Google), and is as simple as we can think of. We have to accept that we'll have to run the JVM on the Git servers.
1. We'll keep using the existing sharding functionality in GitLab to be able to add new servers. Currently we can use it to have multiple file/git servers. Later we will need multiple Git Ketch clusters.
1. We need to get rid of NFS mounting at some point because one broken NFS server causes all the application servers to fail to the point where you can't even ssh in.
1. We want to move the git executable as close to the disk as possible to reduce latency, hence the need for git rpc to talk between the app server and git.
1. [Cached metadata is stored in Redis LRU](https://gitlab.com/gitlab-org/gitaly/issues/2#note_20157141)
1. [Cached payloads are stored in files](https://gitlab.com/gitlab-org/gitaly/issues/14) since Redis can't store large objects
1. Why not use GitLab Git? So workhorse and ssh access can use the same system. We need this to manage cache invalidation.
1. Why not make this a library for most users instead of a daemon/server?
    * Centralization: We need this new layer to be accessed and to share resources from multiple sources. A library is not fit for this end.
    * A library would have to be used in one of our current components, none of which seems ideal to take on this task:
        * gitlab-shell: return to the gitolite model? No.
        * Gitlab-workhorse: is now a proxy for Rails; would then become simultaneous proxy and backend service. Sounds confusing.
        * Unicorn: cannot handle slow requests
        * Sidekiq: can handle slow jobs but not requests
        * Combination workhorse+unicorn+sidekiq+gitlab-shell: this is hard to get right and slow to build even when you are an expert
    * With a library we will still need to keep the NFS shares mounted in the application hosts. That puts a hard stop to scale our storage because we need to keep multiplying the NFS mounts in all the workers.
1. Can we focus on instrumenting first before building Gitaly? Prometheus doesn't work with Unicorn.
1. How do we ship this quickly without affecting users? Behind a feature flag like we did with workhorse. We can update it independently in production.
1. How much memory will this use? Guess 50MB, we will save memory in the rails app, guess more in sidekiq (GBs but not sure), but initially more because more libraries are still loaded everywhere.
1. What packaging tool do we use? [Govendor because we like it more](https://gitlab.com/gitlab-org/gitaly/issues/15)
1. How will the networking work? A unix socket for git operations and TCP for monitoring. This prevents having to build out authentication at this early stage. https://gitlab.com/gitlab-org/gitaly/issues/16
1. We'll include the `/vendor` directory in source control https://gitlab.com/gitlab-org/gitaly/issues/18
1. We will use [E3 from BitBucket to measure performance closely in isolation](https://gitlab.com/gitlab-org/gitaly/issues/34).
1. GitLab already has [logic so that the application servers know which file/git server contains what repository](https://docs.gitlab.com/ee/administration/repository_storages.html), this eliminates the need for a router.
1. Use [gRPC](http://www.grpc.io/) instead of HTTP+JSON. Not so much for performance reasons (Protobuf is faster than JSON) but because gRPC is an RPC framework. With HTTP+JSON we have to invent our own framework; with gRPC we get a set of conventions to work with. This will allow us to move faster once we have learned how to use gRPC.
1. All protocol definitions and auto-generated gRPC client code will be in the gitaly repo. We can include the client code from the rest of the application as a Ruby gem / Go package / client executable as needed. This will make cross-repo versioning easier.
1. Gitaly will expose high-level Git operations, not low-level Git object/ref storage lookups. Many interesting Git operations involve an unbounded number of Git object lookups. For example, the number of Git object lookups needed to generate a diff depends on the number of changed files and how deep those files are in the repository directory structure. It is not feasible to make each of those Git object lookups a remote procedure call.
1. By default all Go packages in the Gitaly repository use the `/internal` directory, unless we explicitly want to export something. The only exception is the `/cmd` directory for executables.
1. GitLab requests should use as few Gitaly gRPC calls as possible. This means it is OK to move GitLab application logic into Gitaly when it saves us gRPC round trips.
1. Defining new gRPC calls is cheap. It is better to define a new 'high level' gRPC call and save gRPC round trips than to chain / combine 'low level' gRPC calls.
1. Why doesn't Gitaly use a Git library like [git2go](https://github.com/libgit2/git2go) or [go-git](https://github.com/src-d/go-git)? We intentionally isolate Git queries in individual processes. We could (and may) use Git libraries to make custom query executables but we seem to get by well enough with regular `git`.
1. Why is Gitaly written in Go? At the time the project started the only practical options were Ruby and Go. We expected to be able to handle more traffic with fewer resources if we used Go. Today (Q3 2019), part of Gitaly is written in Ruby. On the particular Gitaly server that hosts gitlab-org/gitlab-ce, we have a pool of gitaly-ruby processes using a total 20GB of RSS and handling 5 requests per second. The single Gitaly Go process on that machine uses less than 3GB of memory and handles 90 requests per second.
