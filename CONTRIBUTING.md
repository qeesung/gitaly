# Contributing to Gitaly

This document describes requirements for merge requests to Gitaly.

## Style Guide

The Gitaly style guide is [documented in it's own file](STYLE.md)

## Changelog

Any new merge request must contain either a CHANGELOG.md entry or a
justification why no changelog entry is needed. New changelog entries
should be added to the 'UNRELEASED' section of CHANGELOG.md.

If a change is specific to an RPC, start the changelog line with the
RPC name. So for a change to RPC `FooBar` you would get:

> FooBar: Add support for `fluffy_bunnies` parameter

## GitLab CE changelog

We only create GitLab CE changelog entries for two types of merge request:

- adding a feature flag for one or more Gitaly RPC's (meaning the RPC becomes available for trials)
- removing a feature flag for one or more Gitaly RPC's (meaning everybody is using a given RPC from that point on)
