# Repositories used by the Gitaly test suite

Gitaly uses two test repositories. One should be enough but we got a
second one for free when importing code from gitlab-ce.

These repositories get cloned by `make prepare-tests`. They end up in:

-   `internal/testhelper/testdata/data/gitlab-test.git`
-   `internal/testhelper/testdata/data/gitlab-git-test.git`

To prevent fragile tests, we use fixed `packed-refs` files for these
repositories. They get installed by make (see `_support/makegen.go`)
from files in `_support`.

To update `packed-refs` run `git gc` in your test repo and copy the new
`packed-refs` to the right location in `_support`.

## Example:

Let's add a new branch to gitlab-test.

```
git clone internal/testhelper/testdata/data/gitlab-test.git _build/gitlab-test

cd _build/gitlab-test
# make new branch etc.
git push origin my-new-branch # push to local copy of gitlab-test

cd ../..

git -C internal/testhelper/testdata/data/gitlab-test.git push origin refs/heads/my-new-branch # push to public, official copy of gitlab-test
git -C internal/testhelper/testdata/data/gitlab-test.git gc
cp internal/testhelper/testdata/data/gitlab-test.git/packed-refs _support/gitlab-test.git-packed-refs
```
