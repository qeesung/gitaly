## Plan

We use our issues board for keeping our work in progress up to date in a single place. Please refer to it to see the current status of the project.

## Gitaly Team Process

### Gitaly Releases

Gitaly uses [SemVer](https://semver.org) version numbering.

## Branching Model

All tags get created on the `master` branch, except patch releases for
older minor versions. Such patches get an "on-demand stable branch".

### Example:

Suppose we have the following sequence of tags on Gitaly `master`:

-   v6.0.0
-   v5.4.4
-   v5.4.3

Now imagine GitLab `12-3-stable` uses Gitaly 5.4.3 and we have a Gitaly
bug fix we want to include in GitLab `12-3-stable`. We will create an
"on-demand stable branch" in Gitaly for this:

1.  Create `5-4-stable` in Gitaly from the latest 5.4.x tag:
    `git checkout -b 5-4-stable v5.4.4`.
2.  Create Gitaly `v5.4.5` on the `5-4-stable` branch.
