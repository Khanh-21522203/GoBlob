# Lifecycle Policies

Phase 8 adds S3 lifecycle policy support with storage and processor scaffolding.

## What is implemented

- Lifecycle rule model in `goblob/s3api/lifecycle`.
- Rule validation, prefix/tag filtering, expiration and transition evaluation.
- Processor abstraction to apply lifecycle rules to bucket objects.
- S3 lifecycle API endpoints:
  - `PUT /<bucket>?lifecycle`
  - `GET /<bucket>?lifecycle`
  - `DELETE /<bucket>?lifecycle`
- Lifecycle process command:
  - `blob lifecycle.process`

Lifecycle configuration is stored at bucket metadata key `lifecycle.json`.

## Example

```bash
blob lifecycle.process -filer 127.0.0.1:8888
blob lifecycle.process -filer 127.0.0.1:8888 -bucket my-bucket
```
