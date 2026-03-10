# S3 Compatibility

| Operation | Status | Notes |
|-----------|--------|-------|
| PutObject | Yes | Supported |
| GetObject | Yes | Supported |
| DeleteObject | Yes | Supported |
| ListObjectsV2 | Yes | Supported |
| Multipart Upload | Yes | Initiate/UploadPart/Complete/Abort |
| HeadObject | Yes | Supported |
| CopyObject | Partial | Behavior depends on filer metadata path handling |
| Object Tagging | Yes | Basic support |
| Versioning | Partial | Basic marker behavior |
| Object Lock | Partial | Depends on IAM/policy rules |
| Replication | No | Not implemented |
| Analytics | No | Not implemented |
