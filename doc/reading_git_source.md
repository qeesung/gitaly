# Tips for reading Git source code

Although Git has good documentation, sometimes you just need to read the
code to understand how it works. This document collects some tips on how
to approach [Git's source code](https://gitlab.com/gitlab-org/git).

## Audience

This is written for Gitaly developers and GitLab troubleshooters (SRE, support engineer).

## Look at the right version

If you want to understand Git's behavior by reading the source, make
sure you are reading the right source. Find out the Git version of the
system you're investigating and select or check out the appropriate tag
in Git.

## Use a viewer with code search

Online code search is usually not that great compared with code search
in an offline text editor, or on the terminal with `git grep`.

## Look at the tests

If you want to know something that is not clear from the documentation,
sometimes the answer is in the tests. These can be found in the
[`t/` subdirectory](https://gitlab.com/gitlab-org/git/tree/master/t).

In [`t/helper`](https://gitlab.com/gitlab-org/git/tree/master/t/helper)
you can find C executables that expose some Git internal functions that
you normally cannot call directly.

The tests themselves are written in shell script. Instructions for
running them are in
[`t/README`](https://gitlab.com/gitlab-org/git/blob/master/t/README).
However, often you don't have to run a test in order to understand what
it are does.

If you're interested in the workings a particular Git command, try
searching the `t/` directory for it.

## Look at the technical documentation

There is a lot of [technical
documentation](https://gitlab.com/gitlab-org/git/tree/master/Documentation/technical)
in the Git source. If you want to know more about file formats, internal Git API's or
network protocols, this is a good place to start.

## Code organization

The Git subcommands we use to interact with Git are mostly (all?) found
in the `builtin/`
[directory](https://gitlab.com/gitlab-org/git/tree/master/builtin). For
example, `git log` is
[`builtin/log.c`](https://gitlab.com/gitlab-org/git/blob/master/builtin/log.c).

The `.c` files at the top level of the Git repository contain code that
is shared across sub-commands. For example,
[`config.c`](https://gitlab.com/gitlab-org/git/blob/master/config.c)
contains code related to getting and setting Git configuration values.
Contrast this with `builtin/config.c`, which is the sub-command code for
`git config`.

When doing a code search for an error message you sometimes get false
matches in the `po/` directory which contains localizations. You may
want to ignore those or filter them out of your search.

If you are trying to make sense of what some internal Git function does
you can read its definition somewhere in a `*.c` file in the root. There
may also be some extra explanation in the corresponding `*.h` (header)
file; the header files define the API of the corresponding `*.c` file.

## Sub-command source files

### Not all sub-commands are written in C

At the top level of the repository, you will find `*.sh` and `*.perl`
files that implement some of Git's sub-commands. For example,
[`git-bisect.sh`](https://gitlab.com/gitlab-org/git/blob/v2.22.0/git-bisect.sh).

### Main function

If you're used to reading Ruby or Go, the `builtin/*.c` files could be a
little disorienting. This is because the function call graph is ordered
with leaf functions at the top, and the main entrypoint will be at the
bottom. This allows the Git source code to have fewer (or no) forward
declarations of functions.

So if you want to do a top-down walk of a Git sub-command, expect to
find the main entry point at the bottom of the corresponding
`builtin/*.c` file. The entry point for e.g. `git blame` will be called
[`cmd_blame` in
`builtin/blame.c`](https://gitlab.com/gitlab-org/git/blob/v2.22.0/builtin/blame.c#L778).
Recall that hyphens are not allowed in function names, so the entry
point for `git upload-pack` is `cmd_upload_pack`.

Some functions are not where you expect them. For example,
`cmd_format_patch` is in `builtin/log.c`. Use code search!

### Global state

The way we write Ruby and Go at GitLab, it is common to bundle and hide
state in classes (Ruby) or structs (Go). Global state is rare.

Things are different in Git. Builtin commands often use `static`
(i.e. file-scoped) global state. This reduces the number of arguments
that have to be passed to functions, just like having state in a Ruby
class does.

You usually find the global variables at the top of the file.

## C trivia

If you don't use C every day some things about it might be surprising.

### Implicit use of "zero means false"

In Ruby, you will never write `if some_number` because if `some_number`
is a variable containing a number, that `if` is equivalent to `if true`.
In Go, you are not allowed by the compiler to write `if someNumber {`.

However, in C, it is OK to write `if (some_number)`: this is equivalent
to `if (some_number != 0)`. Whether that is OK is a matter of style, and
in Git, you will see that `if (some_number)` is common.

A variation of this has to do with zero-terminated data structures such
as classic C strings, and linked lists. The loop below will visit each
character in the string. Note that the test condition of the loop, `*s`,
will be `0` at the end of the string, and the loop will break.

```C
for (s = "some string"; *s; s++)
```

You will see the same pattern with linked lists, where the test
condition is the pointer to the current element.

```C
for (x = my_list; x; x = x-> next)
```

This becomes even more cryptic if you are dealing with a `while` loop.

```C
while (x)
```
