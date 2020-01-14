# SQL migrations in Praefect

SQL migration files are stored in `/internal/praefect/datastore/migrations`.

The underlying migration engine we use is [github.com/rubenv/sql-migrate](https://github.com/rubenv/sql-migrate).

To generate a new migration, run the `_support/new-migration` script from the top level of your Gitaly checkout.

Praefect SQL migrations should be applied automatically when you deploy Praefect. If you want to run them manually, run:

```
praefect -config /path/to/config.toml sql-migrate
```
