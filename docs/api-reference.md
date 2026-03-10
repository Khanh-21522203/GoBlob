# API Reference

## Master HTTP
- `POST /dir/assign`
- `GET /dir/lookup`
- `GET /dir/status`
- `POST /vol/grow`
- `POST /vol/vacuum`
- `GET /health`
- `GET /ready`

## Volume HTTP
- `PUT /{fid}`
- `GET /{fid}`
- `DELETE /{fid}`
- `GET /status`
- `GET /health`
- `GET /ready`

## Filer HTTP
- `POST /{path...}`
- `GET /{path...}`
- `DELETE /{path...}`
- `GET /health`
- `GET /ready`

## S3 HTTP
- `GET /` (ListBuckets)
- Bucket and object operations via S3-compatible routes
- `GET /health`
- `GET /ready`
