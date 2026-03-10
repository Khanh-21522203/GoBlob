# Filer Store Backends

GoBlob filer supports multiple metadata backends via `-storeBackend` (or `-filer.storeBackend` in all-in-one mode):

- `leveldb2`
- `redis3`
- `postgres2`
- `mysql2`
- `cassandra`

For non-LevelDB backends, pass backend-specific settings with repeated `key=value` flags:

- `blob filer -store.config redis3.address=127.0.0.1:6379`
- `blob server -filer.store.config postgres2.hostname=127.0.0.1`

## Redis3

Required:

- `redis3.address` (default `127.0.0.1:6379`)

Optional:

- `redis3.password`
- `redis3.database` (default `0`)
- `redis3.key_prefix` (default `goblob:filer`)

Example:

```bash
blob filer \
  -storeBackend redis3 \
  -store.config redis3.address=127.0.0.1:6379 \
  -store.config redis3.database=0
```

## Postgres2

Optional keys and defaults:

- `postgres2.hostname` (default `127.0.0.1`)
- `postgres2.port` (default `5432`)
- `postgres2.database` (default `goblob`)
- `postgres2.username` (default `postgres`)
- `postgres2.password` (default empty)
- `postgres2.sslmode` (default `disable`)
- `postgres2.connection_max_idle` (default `10`)
- `postgres2.connection_max_open` (default `100`)
- `postgres2.connection_max_lifetime_seconds` (default `300`)

Example:

```bash
blob filer \
  -storeBackend postgres2 \
  -store.config postgres2.hostname=127.0.0.1 \
  -store.config postgres2.port=5432 \
  -store.config postgres2.database=goblob \
  -store.config postgres2.username=postgres \
  -store.config postgres2.password=secret
```

## MySQL2

Optional keys and defaults:

- `mysql2.hostname` (default `127.0.0.1`)
- `mysql2.port` (default `3306`)
- `mysql2.database` (default `goblob`)
- `mysql2.username` (default `root`)
- `mysql2.password` (default empty)
- `mysql2.params` (extra DSN params, `k=v&k2=v2`)
- `mysql2.connection_max_idle` (default `10`)
- `mysql2.connection_max_open` (default `100`)
- `mysql2.connection_max_lifetime_seconds` (default `300`)

Example:

```bash
blob filer \
  -storeBackend mysql2 \
  -store.config mysql2.hostname=127.0.0.1 \
  -store.config mysql2.port=3306 \
  -store.config mysql2.database=goblob \
  -store.config mysql2.username=root \
  -store.config mysql2.password=secret
```

## Cassandra

Optional keys and defaults:

- `cassandra.hosts` comma-separated hosts (default `127.0.0.1`)
- `cassandra.port` (default `9042`)
- `cassandra.keyspace` (default `goblob`)
- `cassandra.username`
- `cassandra.password`

Example:

```bash
blob filer \
  -storeBackend cassandra \
  -store.config cassandra.hosts=127.0.0.1 \
  -store.config cassandra.port=9042 \
  -store.config cassandra.keyspace=goblob
```

## Notes

- `leveldb2` remains the default backend.
- Transactions are supported on `postgres2` and `mysql2`; `redis3` and `cassandra` use no-op transaction methods.
- If a backend is unreachable at startup, filer startup fails with a backend-specific error.
