# Gitaly-proto release process

## Requirements

- Ruby
- Bundler
- Go 1.10

## 1. Install dependencies

If you have done a release before this may not be needed.

```
_support/install-protoc
```

## 2. Release

This will:

- do a final consistency check
- create a version bump commit
- create a tag
- build the gem
- ask for confirmation
- push the gem and the tag out to the world

```
_support/release 1.2.3
```
