# Git object quarantine during git push

While receiving a Git push, `git receive-pack` will temporarily put
the Git objects sent by the client in a "quarantine directory". These
objects are moved to the proper object directory during the ref update
transaction.

This is a Git implementation detail but it matters to GitLab because
during a push, GitLab performs its own validations and checks on the
data being pushed. That means that GitLab must be able to see
quarantined Git objects.

In this document we will explain how Git object quarantine works, and
how GitLab is able to see quarantined objects.

## Git object quarantine

Git object quarantine was introduced in Git 2.11.0 via https://gitlab.com/gitlab-org/git/-/commit/25ab004c53cdcfea485e5bf437aeaa74df47196d.

From the commit message:

> In order for the receiving end of "git push" to inspect the
> received history and decide to reject the push, the objects sent
> from the sending end need to be made available to the hook and
> the mechanism for the connectivity check, and this was done
> traditionally by storing the objects in the receiving repository
> and letting "git gc" to expire it.  Instead, store the newly
> received objects in a temporary area, and make them available by
> reusing the alternate object store mechanism to them only while we
> decide if we accept the check, and once we decide, either migrate
> them to the repository or purge them immediately.

In the above, "receiving end" means GitLab / Gitaly for us. In GitLab,
inspecting the push is done by the Git `pre-receive` hook.

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
1. `git receive-pack` creates a quarantine directory `objects/incoming-$RANDOM`
1. `git receive-pack` unpacks the objects into the quarantine directory
1. `git receive-pack` runs the `pre-receive` hook with special `GIT_OBJECT_DIRECTORY` and `GIT_ALTERNATE_OBJECT_DIRECTORIES` environment variables that add the quarantine directory to the search path
1. If the `pre-receive` hook rejects the push, `git receive-pack` removes the quarantine directory and its contents. The push is aborted.
1. If the `pre-receive` hook passes, `git receive-pack` merges the quarantine directory into the main object directory and deletes the (now empty) quarantine directory.
1. `git receive-pack` moves to the ref update stage, which includes running the `update` hooks
1. `git receive-pack` performs the rest of its tasks

Note that by the time the `update` hook runs, the quarantine directory
no longer exists! The same goes for the `post-receive` hook which runs
even later.

