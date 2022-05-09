# Cgroups in Gitaly 

## High Level

Gitaly can be configured to run its git processes inside of cgroups, which prevent
shelled-out processes from hogging too much CPU and memory. See [these docs](https://man7.org/linux/man-pages/man7/cgroups.7.html) for a full specification on cgroups.

## Configuration

### Top Level

Here is the top level `[cgroups]` configuration:

```toml
[cgroups]
mountpoint = "/sys/fs/cgroup"
hierarchy_root = "gitaly"
memory_bytes = 64424509440 # 60gb
cpu_shares = 1024
```

**mountpoint** is the top level directory where cgroups will be created.
**hierarchy_root** is the parent cgroup under which Gitaly creates cgroups.
**memory_bytes** limits all processes created by Gitaly to a memory limit,
collectively.
**cpu_shares** limits all processes created by Gitaly to a cpu limit, collectively

### Repository Groups

Cgroups that have a repository-level isolation can also be defined:

```toml
[cgroups.repositories]
count = 10000
memory_bytes = 12884901888 # 12gb
cpu_shares = 512
```

**count** is the number of cgroups to create.
**memory_bytes** limits [memory](#memory-limits) for processes within one cgroup.
This number cannot exceed the top level memory limit.
**cpu_shares** limits [cpu](#cpu-limits) for processes within one cgroup. This
number cannot exceed the top level cpu limit.

These cgroups will be created when Gitaly starts up. A circular hashing algorithm
is used to assign repositories to cgroups. So when  we reach the max number of
cgroups we set in `[cgroups.repositories]`, requests from subsequent repositories
will be assigned to an existing cgroup.

## Memory Limits

Each cgroup has a memory limit which in this example config, is 12gb. All
processes that are part of a cgroup are limited to 12gb of memory collectively.
This means that if there are 5 processes that collectively use up 10gb, and a
6th process is added to the cgroup and its memory slowly climbs beyond 2gb, the
OOM killer will target the process with the highest memory usage to be killed.

This is an oversimplification of how the OOM killer works. A more detailed
picture can be found [here](https://blog.crunchydata.com/blog/deep-postgresql-thoughts-the-linux-assassin#:~:text=CGroup%20Level%20OOM%20Killer%20Mechanics&text=First%20of%20all%2C%20the%20OOM,%2Fcgroup%2Fmemory%2Fmemory.)
and [here](https://lwn.net/Kernel/Index/#OOM_killer).

This provides baseline protection from processes running haywire, such as a
memory leak. However, this only provides limited protection because there are
some legitimate git processes that can take a huge amount of memory such as
`git-repack(1)`, or `git-upload-pack(1)`. For a large repository, these
operations can easily take 12gb of ram as seen in production systems.

Hence, we also need finer grained controls to allow certain expensive git
operations to have their own cgroups.

## CPU Limits

Each cgroup has cpu limits as defined by a concept called cpu shares. By
definition, full usage of a machine's CPU is 1024 shares. Anything lower than
that will be a fraction of the total CPU resources a machine has access to.

## Cgroup Hierarchy

```
/sys/fs/cgroup
|
|--memory
|    |--gitaly
|         |--gitaly-<pid>
|               |--memory.limit_in_bytes
|               |--repos-0
|               |     |--memory.limit_in_bytes
|               |--repos-1
|               |     |--memory.limit_in_bytes
|               |--repos-2
|               |     |--memory.limit_in_bytes
|               |--repos-3
|               |     |--memory.limit_in_bytes
|               |--repos-4
|               |     |--memory.limit_in_bytes
|               |--repos-5
|               |     |--memory.limit_in_bytes
|               |--repos-6
|               |     |--memory.limit_in_bytes
|               |--repos-7
|               |     |--memory.limit_in_bytes
|               |--repos-8
|               |     |--memory.limit_in_bytes
|               |--repos-9
|               |     |--memory.limit_in_bytes
|               |--repos-10
|                     |--memory.limit_in_bytes
|
|-cpu
|  |--gitaly
|        |--gitaly-<pid>
|              |--cpu.shares
|              |--repos-0
|              |     |--cpu.shares
|              |--repos-1
|              |     |--cpu.shares
|              |--repos-2
|              |     |--cpu.shares
|              |--repos-3
|              |     |--cpu.shares
|              |--repos-4
|              |     |--cpu.shares
|              |--repos-5
|              |     |--cpu.shares
|              |--repos-6
|              |     |--cpu.shares
|              |--repos-7
|              |     |--cpu.shares
|              |--repos-8
|              |     |--cpu.shares
|              |--repos-9
|              |     |--cpu.shares
|              |--repos-10
|                    |--cpu.shares
```
