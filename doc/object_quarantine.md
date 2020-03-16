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

