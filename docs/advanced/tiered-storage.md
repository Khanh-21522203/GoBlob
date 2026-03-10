# Tiered Storage

Phase 8 introduces tiering scaffolding under `goblob/storage/tiering`.

## What is implemented

- Tier policy types (`DiskTypeSSD`, `DiskTypeHDD`, ages, remote store).
- Background scanner with periodic `ScanAndTier` execution.
- Rule evaluation:
  - SSD -> HDD migration after `AccessAge`.
  - Local -> remote archival after `ArchiveAge`.
- Admin command: `blob volume.tier.upload` with planning and apply modes.

## Command

```bash
blob volume.tier.upload -source.dir /data/hdd -cloud s3 -bucket archive
```

Apply mode executes upload-copy simulation into a provider/bucket mirror under `-output.dir`:

```bash
blob volume.tier.upload \
  -source.dir /data/hdd \
  -cloud s3 \
  -bucket archive \
  -apply \
  -output.dir ./tier-output
```

Output artifacts:

- Mirrored copied files under `./tier-output/<cloud>/<bucket>/...`
- Upload manifest: `_tier_upload_manifest.json`
