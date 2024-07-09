# `gitaly-backup`

`gitaly-backup` is used to create backups of the Git repository data from
Gitaly and Gitaly Cluster.

## Directly backup repository data

1. For each project to backup, find the Gitaly storage name and relative or disk path using either:
   - The [Admin area](https://docs.gitlab.com/ee/administration/repository_storage_types.html#from-project-name-to-hashed-path).
   - The [repository storage API](https://docs.gitlab.com/ee/api/projects.html#get-the-path-to-repository-storage).

1. Generate the `GITALY_SERVERS` environment variable. This variable specifies
   the address and authentication details of each storage to restore to. The
   variable takes a base64-encoded JSON object.

   For example:

   ```shell
   export GITALY_SERVERS=`echo '{"default":{"address":"unix:/var/opt/gitlab/gitaly.socket","token":""}}' | base64 --wrap=0`
   ```

1. Generate the backup job file. The job file consists of a series of JSON objects separated by a new line (`\n`).

   | Attribute           | Type     | Required | Description |
   |:--------------------|:---------|:---------|:------------|
   |  `address`          |  string  |  no      |  Address of the Gitaly or Gitaly Cluster server. Overrides the address specified in `GITALY_SERVERS`. |
   |  `token`            |  string  |  no      |  Authentication token for the Gitaly server. Overrides the token specified in `GITALY_SERVERS`. |
   |  `storage_name`     |  string  |  yes     |  Name of the storage where the repository is stored. |
   |  `relative_path`    |  string  |  yes     |  Relative path of the repository. |
   |  `gl_project_path`  |  string  |  no      |  Name of the project. Used for logging. |

   For example, `backup_job.json`:

   ```json
   {
     "storage_name":"default",
     "relative_path":"@hashed/f5/ca/f5ca38f748a1d6eaf726b8a42fb575c3c71f1864a8143301782de13da2d9202b.git",
     "gl_project_path":"diaspora/diaspora-client"
   }
   {
     "storage_name":"default",
     "relative_path":"@hashed/6b/86/6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b.git",
     "gl_project_path":"brightbox/puppet"
   }
   ```

1. Pipe the backup job file to `gitaly-backup create`.

   ```shell
   /opt/gitlab/embedded/bin/gitaly-backup create -path $BACKUP_DESTINATION_PATH < backup_job.json
   ```

   | Argument              | Type      | Required | Description |
   |:----------------------|:----------|:---------|:------------|
   |  `-path`              |  string   |  yes     |  Directory where the backup files will be created. |
   |  `-parallel`          |  integer  |  no      |  Maximum number of parallel backups. |
   |  `-parallel-storage`  |  integer  |  no      |  Maximum number of parallel backups per storage. |
   |  `-id`                |  string   |  no      |  Used to determine a unique path for the backup when a full backup is created. |
   |  `-layout`            |  string   |  no      |  How backup files are located. One of `manifest` (default), `pointer`, or `legacy`. |
   |  `-incremental`       |  bool     |  no      |  Indicates whether to create an incremental backup. |
   |  `-server-side`       |  bool     |  no      |  Indicates whether to use server-side backups. |

## Directly restore repository data

1. For each project to restore, find the Gitaly storage name and relative or disk path using either:
   - The [Admin area](https://docs.gitlab.com/ee/administration/repository_storage_types.html#from-project-name-to-hashed-path).
   - The [repository storage API](https://docs.gitlab.com/ee/api/projects.html#get-the-path-to-repository-storage).

1. Generate the `GITALY_SERVERS` environment variable. This variable specifies
   the address and authentication details of each storage to restore to. The
   variable takes a base64-encoded JSON object.

   For example:

   ```shell
   export GITALY_SERVERS=`echo '{"default":{"address":"unix:/var/opt/gitlab/gitaly.socket","token":""}}' | base64 --wrap=0`
   ```

1. Generate the restore job file. The job file consists of a series of JSON objects separated by a new-line (`\n`).

   | Attribute           | Type     | Required | Description |
   |:--------------------|:---------|:---------|:------------|
   |  `address`          |  string  |  no      |  Address of the Gitaly or Gitaly Cluster server. Overrides the address specified in `GITALY_SERVERS`. |
   |  `token`            |  string  |  no      |  Authentication token for the Gitaly server. Overrides the token specified in `GITALY_SERVERS`. |
   |  `storage_name`     |  string  |  yes     |  Name of the storage where the repository is stored. |
   |  `relative_path`    |  string  |  yes     |  Relative path of the repository. |
   |  `gl_project_path`  |  string  |  no      |  Name of the project. Used for logging. |
   |  `always_create`    |  boolean |  no      |  Create the repository even if no bundle for it exists (for compatibility with existing backups). Defaults to `false` |

   For example, `restore_job.json`:

   ```json
   {
     "storage_name":"default",
     "relative_path":"@hashed/f5/ca/f5ca38f748a1d6eaf726b8a42fb575c3c71f1864a8143301782de13da2d9202b.git",
     "gl_project_path":"diaspora/diaspora-client",
     "always_create": true
   }
   {
     "storage_name":"default",
     "relative_path":"@hashed/6b/86/6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b.git",
     "gl_project_path":"brightbox/puppet"
   }
   ```

1. Pipe the restore job file to `gitaly-backup restore`.

   ```shell
   /opt/gitlab/embedded/bin/gitaly-backup restore -path $BACKUP_SOURCE_PATH < restore_job.json
   ```

   | Argument                    | Type                   | Required | Description |
   |:----------------------------|:-----------------------|:---------|:------------|
   |  `-path`                    |  string                |  yes     |  Directory where the backup files are stored. |
   |  `-parallel`                |  integer               |  no      |  Maximum number of parallel restores. |
   |  `-parallel-storage`        |  integer               |  no      |  Maximum number of parallel restores per storage. |
   |  `-id`                      |  string                |  no      |  ID of full backup to restore. If not specified, the latest backup is restored (default). |
   |  `-layout`                  |  string                |  no      |  How backup files are located. One of `manifest` (default), `pointer`, or `legacy`. |
   |  `-remove-all-repositories` |  comma-separated list  |  no      |  List of storage names to have all repositories removed from before restoring. You must specify `GITALY_SERVERS` for the listed storage names. |
   |  `-server-side`             |  bool                  |  no      |  Indicates whether to use server-side backups. |

## Path

Path determines where on the local filesystem or in object storage backup files
are created on or restored from. The path is set using the `-path` flag.

### Local Filesystem

If `-path` specifies a local filesystem, it is the root of where all backup
files are created.

### Object Storage

`gitaly-backup` supports streaming backup files directly to object storage
using the [`gocloud.dev/blob`](https://pkg.go.dev/gocloud.dev/blob) library.
`-path` can be used with:

- [Amazon S3](https://pkg.go.dev/gocloud.dev/blob/s3blob). For example `-path=s3://my-bucket?region=us-west-1`.
- [Azure Blob Storage](https://pkg.go.dev/gocloud.dev/blob/azureblob). For example `-path=azblob://my-container`.
- [Google Cloud Storage](https://pkg.go.dev/gocloud.dev/blob/gcsblob). For example `-path=gs//my-bucket`.

## Layouts

The way backup files are arranged on the filesystem or on object storage is
determined by the layout. The layout is set using the `-layout` flag.

### Legacy layout

This layout is designed to be identical to historic `backup.rake` repository
backups. Repository data is stored in bundle files in a pre-determined
directory structure based on each repository's relative path. This directory
structure is then archived into a tar file by `backup.rake`. Each time a backup
is created, this entire directory structure is recreated.

For example, a repository with the relative path of
`@hashed/4e/c9/4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.git`
creates the following structure:

```text
$BACKUP_DESTINATION_PATH/
  @hashed/
    4e/
      c9/
        4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.bundle
```

#### Generating full backups

A bundle with all references is created via the RPC `CreateBundle`. It
effectively executes the following:

```shell
git bundle create repo.bundle --all
```

#### Generating incremental backups

This layout does not support incremental backups.

### Pointer layout

This layout is designed to support incremental backups. Each repository backup
cannot overwrite a previous backup because this would leave dangling incremental
backups. To prevent dangling incremental backups, every new full backup is put into a new directory.
The two files called `LATEST` point to:

- The latest full backup.
- The latest increment of that full backup.

These pointer files enable looking up
backups from object storage without needing directory traversal (directory
traversal typically requires additional permissions). In addition to the bundle
files, each backup writes a full list of refs and their target object IDs.

When the pointer files are not found, the pointer layout will fall back to
using the legacy layout.

For example, a repository with the relative path of
`@hashed/4e/c9/4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.git`
and a backup ID of `20210930065413` will create the following structure:

```text
$BACKUP_DESTINATION_PATH/
  @hashed/
    4e/
      c9/
        4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a/
          LATEST
          20210930065413/
            001.bundle
            001.refs
            LATEST
```

#### Generating full backups

1. A full list of references is retrieved via the RPC `ListRefs`. This list is written to `001.refs` in the same format as [`git-show-ref`](https://git-scm.com/docs/git-show-ref#_output).
1. A bundle is generated using the retrieved reference names. Effectively, by running:

   ```shell
   awk '{print $2}' 001.refs | git bundle create repo.bundle --stdin
   ```

1. The backup and increment pointers are written.

#### Generating incremental backups

1. The next increment is calculated by finding the increment `LATEST` file and
   adding 1. For example, `001` + `1` = `002`.
1. A full list of references is retrieved using the `ListRefs` RPC. This list is
   written to the calculated next increment (for example, `002.refs`) in the same
   format as [`git-show-ref`](https://git-scm.com/docs/git-show-ref#_output).
1. The full list of the previous increments references is retrieved by reading
   the file. For example, `001.refs`.
1. A bundle is generated using the negated list of reference targets of the
   previous increment and the new list of retrieved reference names
   by effectively running:

   ```shell
   { awk '{print "^" $1}' 001.refs; awk '{print $2}' 002.refs; } | git bundle create repo.bundle --stdin
   ```

   Negating the object IDs from the previous increment ensures that we stop
   traversing commits when we reach the HEAD of the branch at the time of the
   last incremental backup.

### Manifest layout

This layout uses manifest files to describe where each file exists in a backup
to restore the files from the backup:

- The latest backup has two manifests, one named `+latest.toml` and another named after
  its backup ID. `+latest.toml` is overwritten by newer backups.
- Every other backup has one manifest named after the backup ID.

For example, the following structure is created for a repository with the:

- Storage name of `default`.
- Relative path of `@hashed/4e/c9/4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.git`.
- Backup ID of `20210930065413`.

```plaintext
$BACKUP_DESTINATION_PATH/
  manifests/
    default/
      @hashed/
        4e/
          c9/
            4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.git/
              20210930065413.toml
              +latest.toml
  default/
    @hashed/
      4e/
        c9/
          4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.git/
            20210930065413/
              001.bundle
              001.refs
```

If an incremental backup was then created with a backup ID of `20211001065419`,
`gitaly-backup` would create the following structure:

```plaintext
$BACKUP_DESTINATION_PATH/
  manifests/
    default/
      @hashed/
        4e/
          c9/
            4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.git/
              20210930065413.toml
              20211001065419.toml
              +latest.toml
  default/
    @hashed/
      4e/
        c9/
          4ec9599fc203d176a301536c2e091a19bc852759b255bd6818810a42c5fed14a.git/
            20210930065413/
              001.bundle
              001.refs
            20211001065419/
              002.bundle
              002.refs
```

In this case, the manifest file `20211001065419.toml` would reference all the
bundle files required to restore the repository.

## Server-side backups

`gitaly-backup` usually performs a "client-side backup":

- `gitaly-backup` receives a stream of backup data from Gitaly.
- `gitaly-backup` then sends a stream of backup data to the location specified by `-path`.

When `-path` is set to object storage, the entire backup is transferred over the network twice. Once from Gitaly to `gitaly-backup` and again from `gitaly-backup`
to object storage.

To make this more efficient, server-side backups stream backup data directly from the Gitaly to object storage.

To use server-side backups:

1. Add the `[backup]` section in `gitaly.toml` for each Gitaly node to set the server-side backup destination.

   For example, `gitaly.toml`:

   ```toml
   [backup]
   go_cloud_url = "gs://gitaly-backups"
   # layout = "manifest"
   ```

1. Add the `-server-side` flag when invoking `gitaly-backup`. The `-path` and `-layout` flags cannot be used in server-side mode.

   For example:

   ```shell
   /opt/gitlab/embedded/bin/gitaly-backup create -server-side < backup_job.json
   ```

Server-side backups use the [`gocloud.dev/blob`](https://pkg.go.dev/gocloud.dev/blob) library.
`go_cloud_url` can be used with:

- [Amazon S3](https://pkg.go.dev/gocloud.dev/blob/s3blob). For example `go_cloud_url = "s3://my-bucket?region=us-west-1"`.
- [Azure Blob Storage](https://pkg.go.dev/gocloud.dev/blob/azureblob). For example `go_cloud_url = "azblob://my-container"`.
- [Google Cloud Storage](https://pkg.go.dev/gocloud.dev/blob/gcsblob). For example `go_cloud_url = "gs//my-bucket"`.
