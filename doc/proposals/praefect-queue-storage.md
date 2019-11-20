# Storage for Praefect's replication queue

## Rationale

Praefect is the traffic router and replication manager for Gitaly HA.
Praefect is under development and far from being a minimum viable HA
solution. The router detects Gitaly calls that modify repositories, and
submits jobs to a job queue indicating that the repository that got
modified needs to have its replicas updated. The replication manager
consumes the job queue. Currently, this queue is implemented in-memory
in the Praefect process.

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
replication" afterwards. A queue is just a natural way of modeling this
communication pattern.

## Does the queue need to have special properties?

Different types of queues make different trade-offs in their semantics
and reliability. For our purposes, the most important thing is that
**messages get delivered at least once**. Delivering more than once is
wasteful but otherwise harmless: this is because we are doing idempotent
Git fetches.

If a message gets lost, that can lead to data loss.

## What sort of throughput do we expect?

Currently (November 2019), gitlab.com has about 5000 Gitaly calls per
second. A [loose
analysis](https://prometheus.gprd.gitlab.net/graph?g0.range_input=1h&g0.expr=gitaly%3Agrpc_server_handled_total%3Arate1m%7B%20grpc_method!~%22.*TreeEntr.*%22%2Cgrpc_method!~%22.*Ancestor.*%22%2C%20grpc_method!~%22.*(Get%7CDiff%7CExists%7CUpload%7CFind%7CList%7CCount%7CStats%7CHasLocal%7CLastCommit%7CDelta%7CFilter%7CLanguage%7CServerInfo).*%22%2Cgrpc_service!~%22.*.v1.Health%22%7D&g0.tab=0)
suggests about 10% would cause replication jobs to be created. So if we
had one "queue database" behind Praefect on gitlab.com, we would be
inserting 500 jobs per second.

Note that we have room to manoeuver with sharding. Contrary to the SQL
database of GitLab itself, which is more or less monolithic across all
projects, there is no functional requirement to co-locate any two
repositories on the same Gitaly server, nor on the same Praefect
cluster. So if you have 1 million repos, you could make 1 million
Praefect clusters, with 1 million queue database instances (one behind
each Praefect cluster). Each queue database would then see a very, very
low job insertion rate.

This scenario is unpractical from an operation standpoint, but
functionally, it would be OK. In other words, we should never be forced
to vertically scale the queue database. There will of course be
practical limits on how many instances of the queue database we can run.
Especially because the queue database must be highly available.

## The queue database must be highly available

If the queue database is unavailable, Praefect should be forced into a
read-only mode. This is undesirable, so I think we can say we want the
queue database to be highly available itself.
