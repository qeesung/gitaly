# Git object quarantine during git push

While receiving a Git push, GitLab can reject pushes using the `pre-receive` Git hook. Git has a special "object quarantine" mechanism that allows it to eagerly delete reject Git objects.

In this document we will explain how Git object quarantine works, and
how GitLab is able to see quarantined objects.

## Git object quarantine

Git object quarantine was introduced in Git 2.11.0 via
https://gitlab.com/gitlab-org/git/-/commit/25ab004c53cdcfea485e5bf437aeaa74df47196d.
To understand what it does we need to know how Git receives pushes on
the server.

### How Git receives a push

On a Git server, a push goes into `git receive-pack`. This process does the following things:

- receive the Git objects pushed by the client and write them to disk
- receive the ref update commands from the client and keep them in memory
- check connectivity (no missing objects)
- run `pre-receive` and feed it the intended ref update commands
- if `pre-receive` rejects the push, clean up and stop
- apply ref update commands one by one. For each command, run the `update` hook which can reject the ref update.
- after all ref updates have been applied run the `post-receive` hook
- report success to the client and end the session

Object quarantine exists for the sake of the cleanup that happens when
`pre-receive` rejects the push. It changes the _timing_ of the
cleanup. Without object quarantine, objects that were part of a
rejected push would sit around until `git gc` would judge them as both
unused and "old". How long that takes depends on how often `git gc`
runs (or `git prune`), and on the configuration of when objects are
"old". Because of object quarantine, rejected objects can be deleted
immediately: Git can just `rm -rf` the quarantine directory and
they're gone.

### Git implementation

The Git implementation of this mechanism rests on two things.

#### 1. Alternate object directories

The objects in a Git repository can be stored across multiple
directories: 1 main directory, usually `/objects`, and 0 or more
alternate directories. Together these act like a search path: when
looking for an object Git first checks the main directory, then each
alternate, until it finds the object.

#### 2. Config overrides via environment variables

Git can inject custom config into subprocesses via environment
variables. In the case of Git object directories, these are
`GIT_OBJECT_DIRECTORY` (the main object directory) and
`GIT_ALTERNATE_OBJECT_DIRECTORIES` (a search path of `:`-separated
alternate object directories).

#### Putting it all together

1. `git receive-pack` receives a push
1. `git receive-pack` [creates a quarantine directory `objects/incoming-$RANDOM`](https://gitlab.com/gitlab-org/git/-/blob/v2.24.0/builtin/receive-pack.c#L1715)
1. `git receive-pack` [configures the unpack process](https://gitlab.com/gitlab-org/git/-/blob/v2.24.0/builtin/receive-pack.c#L1721) to write objects into the quarantine directory
1. `git receive-pack` unpacks the objects into the quarantine directory
1. `git receive-pack` [runs the `pre-receive` hook](https://gitlab.com/gitlab-org/git/-/blob/v2.24.0/builtin/receive-pack.c#L1498) with special `GIT_OBJECT_DIRECTORY` and `GIT_ALTERNATE_OBJECT_DIRECTORIES` environment variables that add the quarantine directory to the search path
1. If the `pre-receive` hook rejects the push, `git receive-pack` removes the quarantine directory and its contents. The push is aborted.
1. If the `pre-receive` hook passes, `git receive-pack` [merges the quarantine directory into the main object directory](https://gitlab.com/gitlab-org/git/-/blob/v2.24.0/builtin/receive-pack.c#L1510).
1. `git receive-pack` enters the ref update transaction

Note that by the time the `update` hook runs, the quarantine directory
has already been merged into the main object directory so it no longer
matters. The same goes for the `post-receive` hook which runs even
later.

Because `pre-receive` has the special quarantine configuration data in
environment variables, any `git` process spawned by `pre-receive` will
inherit the quarantine config and will be able to see the objects that
are being pushed.

## GitLab and Git object quarantine

### Why does all this matter to GitLab

GitLab uses Git hooks, among other things, to implement features that
can reject Git pushes. For example, you can mark a branch as
"protected" in the GitLab web UI, and then certain types of users can
no longer push to that branch. That feature is implemented via the [Git
`pre-receive` hook](https://gitlab.com/gitlab-org/gitaly/-/blob/969bac80e2f246867c1a976864bd1f5b34ee43dd/ruby/gitlab-shell/hooks/pre-receive).

As mentioned above, Git object quarantine normally works more or less
automatically because `git` commands spawned by the `pre-receive` hook
inherit the special environment variables that contain the path to the
quarantine directory. In the case of GitLab's hooks we have a problem,
however, because the GitLab hooks are "dumb". All the GitLab hooks do
is take the inputs of the hook executable (the list of ref update
commands) and send them to the GitLab Rails internal API via a POST
request. The application logic that decides whether the push is
allowed resides in Rails. The hook just waits and reports back result
of the POST API request to GitLab.

During the POST, the internal GitLab API makes Gitaly calls back into the repo to
examine the objects being pushed. For example, if force pushes are not
allowed, GitLab will call the IsAncestor RPC. That RPC call then wants
to look at a commit that is in the process of being pushed. But
because that commit is in quarantine, the RPC will fail because the
commit cannot be found.

### How GitLab passes the object quarantine information around

To overcome this problem, the GitLab `pre-receive` hook [reads the
object directory configuration from its
environment](https://gitlab.com/gitlab-org/gitaly/-/blob/969bac80e2f246867c1a976864bd1f5b34ee43dd/ruby/gitlab-shell/lib/object_dirs_helper.rb#L3),
and passes this information [along with the HTTP API
call](https://gitlab.com/gitlab-org/gitaly/-/blob/969bac80e2f246867c1a976864bd1f5b34ee43dd/ruby/gitlab-shell/lib/gitlab_access.rb#L24).
On the Rails side, we then [put the object directory information in
the "request
store"](https://gitlab.com/gitlab-org/gitlab/-/blob/master/lib/api/internal/base.rb#L43)
(i.e., request-scoped thread-local storage). And then during that
Rails request, when Rails makes Gitaly requests on this repo, we send
back the quarantine information [in the Gitaly `Repository`
struct](https://gitlab.com/gitlab-org/gitlab/-/blob/f81f30c29a0edce20f6737fdccc3315c8baab9d1/lib/gitlab/gitaly_client/util.rb#L8-17).
And finally, inside Gitaly, when we spawn a Git process, we [re-create
the environment
variables](https://gitlab.com/gitlab-org/gitaly/-/blob/969bac80e2f246867c1a976864bd1f5b34ee43dd/internal/git/alternates/alternates.go#L21-34)
that were present on the `pre-receive` hook, so that we can see the
quarantined objects. We do the same when we [instantiate a
Gitlab::Git::Repository in
gitaly-ruby](https://gitlab.com/gitlab-org/gitaly/-/blob/969bac80e2f246867c1a976864bd1f5b34ee43dd/ruby/lib/gitlab/git/repository.rb#L44).

### Relative paths

During the Gitaly migration we had to handle a complication with the
object quarantine information: Git uses absolute paths for this. These
paths get generated wherever `git receive-pack` runs, i.e., on the
Gitaly server. During the migration, the repositories were also
accessible via NFS at the Rails side, but at a different path. That
meant that the absolute paths supplied by Git would be invalid part of
the time.

To work around this, the GitLab `pre-receive` hook [converts the
relative paths from Git into relative
paths](https://gitlab.com/gitlab-org/gitaly/-/blob/969bac80e2f246867c1a976864bd1f5b34ee43dd/ruby/gitlab-shell/lib/object_dirs_helper.rb#L16),
relative to the repository directory. These relative paths then get
passed around inside GitLab. At the time Gitaly recreates the object
directory variables, it [converts the paths back from relative to
absolute](https://gitlab.com/gitlab-org/gitaly/-/blob/969bac80e2f246867c1a976864bd1f5b34ee43dd/internal/git/alternates/alternates.go#L23).
