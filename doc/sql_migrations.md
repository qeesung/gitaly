# SQL migrations in Praefect

SQL migration files are stored in `/internal/praefect/datastore/migrations`.

The underlying migration engine we use is [github.com/rubenv/sql-migrate](https://github.com/rubenv/sql-migrate).

To generate a new migration, run the `_support/new-migration` script from the top level of your Gitaly checkout.

Praefect SQL migrations should be applied automatically when you deploy Praefect. If you want to run them manually, run:

```
praefect -config /path/to/config.toml sql-migrate
```

By default, the migration will ignore any unknown migrations that are
not known by the Praefect binary.

The `-ignore-unknown=false` will disable this behavior:

```shell
praefect -config /path/to/config.toml sql-migrate -ignore-unknown=false
```

## Showing the status of migrations

To see which migrations have been applied, run:

```
praefect -config /path/to/config.toml sql-migrate-status
```

For example, the output may look like:

```
+----------------------------------------+--------------------------------------+
|               MIGRATION                |               APPLIED                |
+----------------------------------------+--------------------------------------+
| 20200109161404_hello_world             | 2020-02-26 16:00:32.486129 -0800 PST |
| 20200113151438_1_test_migration        | 2020-02-26 16:00:32.486871 -0800 PST |
| 20200224220728_job_queue               | 2020-03-25 16:27:21.384917 -0700 PDT |
| 20200324001604_add_sql_election_tables | no                                   |
| 20200401010230_add_some_table          | unknown migration                    |
+----------------------------------------+--------------------------------------+
```

The first column contains the migration ID, and the second contains one of three items:

1. The date on which the migration was applied
2. `no` if the migration has not yet been applied
3. `unknown migration` if the migration is not known by the current Praefect binary

## Rolling back migrations

Rolling back SQL migrations in Praefect works a little differently
from ActiveRecord. It is a three step process.

### 1. Decide how many steps you want to roll back

Count the number of migrations you want to roll back.

### 2. Perform a dry run and verify that the right migrations are getting rolled back

```
praefect -config /path/to/config.toml sql-migrate-down NUM_ROLLBACK
```

This will perform a dry run and print the list of migrations that
would be rolled back. Verify that these are the migrations you want to
roll back.

### 3. Perform the rollback

We use the same command as before, but we pass `-f` to indicate we
want destructive changes (the rollbacks) to happen.

```
praefect -config /path/to/config.toml sql-migrate-down -f NUM_ROLLBACK
```
