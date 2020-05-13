# Storage for Praefect's replication queue

## Rationale

Praefect is the traffic router and replication manager for Gitaly Clusters.
Praefect is currently (November 2019) under development and far from
being a minimum viable HA solution. We are at a point where we think we
need to add database to Praefect's architecture.

The router part of Praefect detects Gitaly calls that modify
repositories, and submits jobs to a job queue indicating that the
repository that got modified needs to have its replicas updated. The
replication manager part consumes the job queue. Currently, this queue
is implemented in-memory in the Praefect process.

While useful for prototyping, this is unsuitable for real HA Gitaly for
two reasons:

1.  The job queue must be **persistent**. Currently, the queue is
    emptied if a Praefect process restarts. This can lead to data loss
    in case we fail over away from a repository that is ahead of its
    replicas.
2.  The job queue must be **shared**. We expect multiple Praefect
    processes to be serving up the same Gitaly storage cluster. This is
    so that Praefect itself is not a single point of failure. These
    Praefect processes must all see and use the same job queue.

## Does it have to be a queue?

We don't strictly need a queue. We need a shared, persistent database
that allows the router to mark a repository as being in need of
replication, and that allows the replication manager to query for
repositories that need to be replicated -- and to clear them as "needing
replication" afterwards. A queue is just a way of modeling this
communication pattern.

## Does the queue database need to have special properties?

Different types of databases make different trade-offs in their semantics
and reliability. For our purposes, the most important thing is that
**messages get delivered at least once**. Delivering more than once is
wasteful but otherwise harmless: this is because we are doing idempotent
Git fetches.

If a message gets lost, that can lead to data loss.

## What sort of throughput do we expect?

Currently (November 2019), gitlab.com has about 5000 Gitaly calls per
second. About 300 of those [are labeled as
"mutators"](https://prometheus.gprd.gitlab.net/graph?g0.range_input=7d&g0.expr=sum(rate(gitaly_cacheinvalidator_optype_total%5B5m%5D))%20by%20(type)&g0.tab=0),
which suggests that today we'd see about 300 replication jobs per
second. Each job may need multiple writes as it progresses through
different states; say 5 state changes. That makes 1500 writes per
second.

Note that we have room to maneuver with sharding. Contrary to the SQL
database of GitLab itself, which is more or less monolithic across all
projects, there is no functional requirement to co-locate any two
repositories on the same Gitaly server, nor on the same Praefect
cluster. So if you have 1 million repos, you could make 1 million
Praefect clusters, with 1 million queue database instances (one behind
each Praefect cluster). Each queue database would then see a very, very
low job insertion rate.

This scenario is unpractical from an operational standpoint, but
functionally, it would be OK. In other words, we have horizontal leeway
to avoid vertically scaling the queue database. There will of course be
practical limits on how many instances of the queue database we can run.
Especially because the queue database must be highly available.

## The queue database must be highly available

If the queue database is unavailable, Praefect should be forced into a
read-only mode. This is undesirable, so I think we can say we want the
queue database to be highly available itself.

## Running the queue database should be operationally feasible

As always at GitLab, we want to choose solutions that are suitable for
self-managed GitLab installations.

-   Should be open source
-   Don't pick an open core solution, and rely on features that are not
    in the core
-   Don't assume that "the cloud" makes problems go away; assume there
    is no cloud
-   Running the queue database should require as little expertise as
    possible, or it should be a commodity component

## Do we have other database needs in Praefect?

This takes us into YAGNI territory but it's worth considering.

Praefect serves as a front end for a cluster of Gitaly servers (the
"internal Gitaly nodes") that store the actual repository data. We will
need some form of consensus over which internal Gitaly nodes are good
(available) or bad (offline). This is not a YAGNI, we will need this.
Like the queue this would be shared state. The most natural fit for
this, within GitLab's current architecture, would be Consul. But Consul
is not a good fit for storing the queue.

We might want Praefect to have a catalogue of all repositories it is
storing. With Gitaly, there is no such catalogue; the filesystem is the
single source of truth. This strikes me as a YAGNI though. Even with
Praefect, there will be filesystems "in the back" on the internal Gitaly
nodes, and those could serve as the source of truth.

## What are our options

### Redis

Pro:

-   Already used in GitLab
-   Has queue primitives

Con:

-   Deployed with snapshot persistence (RDB dump) in GitLab, which is
    not the durability I think we want

### Postgres

Pro:

-   Already used in GitLab
-   Gold standard for persistence
-   General purpose database: likely to be able to grow with us as we
    develop other needs

Con:

-   Can be used for queues, but not meant for it
-   Need to find queueing library, or develop SQL-backed queue ourselves
    (hard, subtle)
-   Because not meant to be a queue, may have a lower ceiling where we
    are forced to scale horizontally. When we hit the ceiling we would
    have to run multiple Praefect clusters each with their own HA
    Postgres cluster behind it)

### Kafka

Pro:

-   Closely matches description of "durable queue"

Con:

-   Would be new to GitLab: no development experience nor operational
    experience

### SQLite or BoltDB

Embedded databases such as SQLite or BoltDB don't meet our requirements
because we need shared access. Being embedded implies you don't have to
go over a network, while going over a network is an essential feature
for us: this enables us to have multiple machines running Praefect.

### Consul

Consul is something that GitLab already relies on. You could consider it
a database although it is not presented as that by it authors. The
advertised use cases are service discovery and having service mesh.

Consul does contain a key-value store you can use to store values
smaller than 512KB in. But the [documentation
states](https://www.consul.io/docs/install/performance.html#memory-requirements):

> NOTE: Consul is not designed to serve as a general purpose database,
> and you should keep this in mind when choosing what data are populated
> to the key/value store.

## Conclusion

I am strongly leaning towards Postgres because it seems like a safe,
boring choice. It has strong persistence and it is generic, which is
useful because we don't know what our needs are yet.

Running your own HA Postgres is challenging but it's a challenge you
need to take on anyway when you deploy HA GitLab.
