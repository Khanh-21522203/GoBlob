# Quota Management

Phase 8 adds quota enforcement with filer-KV persistence via `goblob/quota` and S3 API wiring.

## What is implemented

- Per-user and per-bucket quota model (`max_bytes`, `used_bytes`).
- KV keys:
  - `quota:user:<id>`
  - `quota:bucket:<name>`
- Quota checks and usage accounting helpers.
- S3 `PUT Object` enforcement with `507 Insufficient Storage` on exceed.
- S3 best-effort usage accounting updates on put/delete.
- CLI commands:
  - `blob quota.set`
  - `blob quota.get`

## Examples

```bash
blob quota.set -filer 127.0.0.1:8888 -user alice -max_bytes 1048576
blob quota.get -filer 127.0.0.1:8888 -user alice
blob quota.set -filer 127.0.0.1:8888 -bucket photos -max_bytes 524288000
blob quota.get -filer 127.0.0.1:8888 -bucket photos
```
