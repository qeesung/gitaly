## Delta Islands

Most of the time, an object in a Git repository is stored as a delta against
another object. This means that if a blob is stored once, and only one line is
changed, the storage requirements for the repository does not come close to the
two individual blobs. This helps Git also, when a client is requesting data during
a fetch. The transmitted bytes again, is much less than the combined size of all
the objects.

Now when a third blob of the same file is created, it too has its delta
calculated against the second blob, only stored as delta, to itself. These three
blobs now form a delta chain. Git stores these delta chains in [pack files][git-pack],
and when a `repack` is executed these chains might be recalculated if one of the
blobs isn't required anymore, or if a better delta base is discovered.

In the case of a git fetch, it might happen that the client doesn't have a set
of branches that, during repack, were detected to share many of the same contents
and thus form a delta chain. Git can then decide to send the full delta chain.

In practice, these delta chains jump between branches, tags, and other refs. When
a client initiates a fetch, it's usually not interested in any of the other
refs. Furthermore it might create a security issue when objects are shared
between repositories. This will invalidate the delta chain on disk, and during
the fetch request, Git will recalculate the diffs for the objects later in the
chain it does want.

Delta islands try to solve this by creating islands of objects which the delta
detection algorithm can use to create a delta against. For example, all branches
could be a namespace, or island. When a client fetches, the likelihood of the
chain being valid is much greater. This prevents Git from reconstructing the
full objects, which improves the load on the server and latency for the fetch.

The drawback of this feature is that the packs on disk are potentially
larger as it's not always the case the optimal object can be used as delta base.

### In GitLab

Delta Island relies on Git version 2.20 or later, which GitLab is expected to
use from version 11.11 onwards. The change on the Gitaly side is limited to
setting a config option [when repacking][delta-config]. This option is set at
runtime to prevent having to write the configuration file for all repositories.

User impact of this feature includes faster fetches, as Git on the server does
less work and reuses previous work better.

#### Further reading

As is usually the case, the [tests of Git][git-delta-test] provide a good overview
of how the feature works.

[git-delta-test]: https://github.com/git/git/blob/041f5ea1cf987a4068ef5f39ba0a09be85952064/t/t5320-delta-islands.sh
[git-pack]: https://git-scm.com/docs/git-pack-objects
[delta-mr]: https://gitlab.com/gitlab-org/gitaly/merge_requests/1110
[delta-config]: https://gitlab.com/gitlab-org/gitaly/merge_requests/1110/diffs#e01aecd9d7ee43aee1959795092f852d07a1e7ed_55_78

