# Gitaly Hooks

Gitaly allows Git to execute hooks after certain mutator RPCs. This document explains the different code paths that trigger hooks.

# git-receive-pack

When git-receive-pack gets called either through `PostReceivePack` or `SSHReceivePack`, git will look for hooks in `core.hooksPath`. See the [githooks documentation](https://git-scm.com/docs/githooks)
for detailed information about how git calls hooks. `core.hooksPath` is set to an internal directory containing Gitaly hooks. Details below.

## Gitaly server hooks

When `git-receive-pack` is called, the following server side hooks run:

`pre-receive`: checks if the ref update is allowed to happen, increments the reference counter. If reference transactions are enabled,
the hook's standard input containing all reference updates will be hashed and submitted as a vote.

`update`: in GitLab, this is a noop. It simply runs the update custom hooks. Custom hooks are detailed below

`post-receive`: prints out the MR link, decreases the reference counter.

Note: The reference counter is a counter per repository so GitLab knows when a certain repository can be moved. If the reference
counter is not at 0, that means there are active pushes happening.

## Custom Hooks

After each hook type `pre-receive`, `update`, `post-receive`, custom hooks are also run. See the [GitLab Server Hooks documentation](https://docs.gitlab.com/ee/administration/server_hooks.html) for how they are used.

### Execution path

A Brief History: Gitaly hooks were originally in GitLab-Shell and implemented in Ruby. They have since been moved to Gitaly, and we are currently in process of replacing these Ruby
hooks with Go implementations.

```mermaid
graph TD
  A[1. git-receive-pack] --> B1[ruby/git-hooks/pre-receive]
  A --> B2[ruby/git-hooks/update]
  A --> B3[ruby/git-hooks/post-receive]
  B1 --> C[2. ruby/git-hooks/gitlab-shell-hook]
  B2 --> C[2. ruby/git-hooks/gitlab-shell-hook]
  B3 --> C[2. ruby/git-hooks/gitlab-shell-hook]
  C --> D[3. GITALY_BIN_DIR/gitaly-hooks]
  D --> E1[4. /gitaly.HookService/PreReceiveHook]
  D --> E2[4. /gitaly.HookService/UpdateHook]
  D --> E3[4. /gitaly.HookService/PostReceiveHook]
  E1 --> F1[5. GitLab Rails /internal/allowed]
  E1 --> F2[6. GitLab Rails /internal/pre_receive]
  E1 --> H1[7. Execute pre-receive custom hooks]
  E2 --> F3[8. update custom hooks]
  E3 --> F4{9. is gitaly_go_postreceive_hook enabled?}
  F4 --> |yes| G3a[10. use go implementation in PostReceiveHook]
  F4 --> |no| G3b[11. ruby/gitlab-shell/hooks/post-receive]
  G3a --> I[12. GitLab Rails /internal/post_receive]
  G3b --> I
  G3a --> J[13. post-receive custom hooks]
  G3b --> J
```

1. `git-receive-pack` calls `pre-receive`, `update`, `post-receive` shell scripts under `ruby/git-hooks/`.
2. Each of these scripts simply calls `ruby/git-hooks/gitlab-shell-hook` with the environment, stdin, hook name, and hook arguments.
3. `ruby/git-hooks/gitlab-shell-hook` in turn calls the `GITALY_BIN_DIR/gitaly-hooks` binary.
4. `gitaly-hooks` will call the corresponding RPC that handles `pre-receive`(`PreReceiveHook`), `update`(`UpdateHook`), and `post-receive`(`PostReceiveHook`). Note: `UpdateHook` will run once per reference that's being updated.
5. `PreReceiveHook` RPC will call out to GitLab's `internal/allowed` endpoint.
6. `PreReceiveHook` RPC will call out to GitLab's `internal/pre_receive` endpoint.
7. `PreReceiveHook` RPC will find `pre-receive` custom hooks and execute them.
8. `UpdateHook` RPC will find `update` custom hooks and execute them.
9. `PostReceiveHook`  will check if `gitaly_go_postreceive_hook` feature flag is enabled.
10. If `gitaly_go_postreceive_hook`  is not enabled, `PostReceiveHook` RPC will fall back to calling the Ruby hook.
11. If `gitaly_go_postreceive_hook` is enabled,  `PostReceiveHook` RPC will use the go implementation.
12. Both the Ruby `post-receive` hook as well as the Go implementation of `PostReceiveHook` will call out to GitLab's `internal/post_receive` endpoint. (the `internal/post_receive` endpoint decreases the reference counter, and generates the MR creation link that gets printed out to stdout.)
13. Both the Ruby `post-receive` hook as well as the Go implementation of `PostReceiveHook` will call the `post-receive` custom hooks.

Note: `gitaly_go_postreceive_hook` has been enabled on production.

# Operations RPCs

The other way that Gitaly hooks are triggered is through the Operations Service RPCs. Similar to `git-receive-pack`, the `pre-receive`, `update`, and `post-receive` are executed when a ref is updated via
one of the Operations Service RPCs. The execution path is different. Instead of `git-receive-pack` triggering the hooks, they are invoked manually
through Gitaly.

### Execution path

```mermaid
graph TD
  A[1. OperationsService RPC] --> B[2. ruby/lib/gitlab_server/operation_service.rb]
  B --> O1[3. with_hooks ]
  O1 --> O2[4. ruby/lib/gitlab/git/hooks_service.rb]
  O2 --> |pre-receive|O4[ruby/lib/gitlab/git/hooks.rb]
  O2 --> |update|O4[ruby/lib/gitlab/git/hooks.rb]
  O2 --> |post-receive|O4[ruby/lib/gitlab/git/hooks.rb]
  O4 --> B1[5. ruby/git-hooks/pre-receive]
  O4 --> B2[5. ruby/git-hooks/update]
  O4 --> B3[5. ruby/git-hooks/post-receive]
  B1 --> C[6. ruby/git-hooks/gitlab-shell-hook]
  B2 --> C[6. ruby/git-hooks/gitlab-shell-hook]
  B3 --> C[6. ruby/git-hooks/gitlab-shell-hook]
  C --> D[7. GITALY_BIN_DIR/gitaly-hooks]
  D --> E1[8. /gitaly.HookService/PreReceiveHook]
  D --> E2[8. /gitaly.HookService/UpdateHook]
  D --> E3[8. /gitaly.HookService/PostReceiveHook]
  E1 --> F1[9. GitLab Rails /internal/allowed]
  E1 --> F2[10. GitLab Rails /internal/pre_receive]
  E1 --> H1[11. Execute pre-receive custom hooks]
  E2 --> F3[12. update custom hooks]
  E3 --> F4{13. is gitaly_go_postreceive_hook enabled?}
  F4 --> |yes| G3a[14. use go implementation in PostReceiveHook]
  F4 --> |no| G3b[15. ruby/gitlab-shell/hooks/post-receive]
  G3a --> I[16. GitLab Rails /internal/post_receive]
  G3b --> I
  G3a --> J[17. post-receive custom hooks]
  G3b --> J
```

1. An OperationsService RPC calls out to `gitaly-ruby`'s `operation_service.rb`.
2. A number of operation service methods call out to the `with_hooks` method.
3. `with_hooks` calls out to `hooks_service.rb`.
4. `hooks_service.rb` calls `hooks.rb` with `pre-receive`, `update`, and `post-receive`.
5. `pre-receive`, `update`, `post-receive` shell scripts call `gitlab-shell-hook` shell script with environment, hook name, hook arguments.
Steps 6-17 are identical to descriptions of steps 2-13 above.
