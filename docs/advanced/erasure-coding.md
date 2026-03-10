# Erasure Coding

Phase 8 adds a Reed-Solomon based erasure-coding package in `goblob/storage/erasure_coding`.

## What is implemented

- `ECVolume` and `ECShard` placement metadata types.
- `Encoder` with configuration validation (`dataShards > 0`, `parityShards >= 0`).
- `Encode` for data+parity shard generation.
- `Decode` / `DecodeWithLength` for reconstruction and integrity verification.
- CLI command: `blob volume.ec.encode` with planning and apply modes.

## Command

```bash
blob volume.ec.encode -vid 5 -dataShards 10 -parityShards 3 -nodes node1:8080,node2:8080,...
```

Apply mode executes shard generation from source volume needles:

```bash
blob volume.ec.encode \
  -vid 5 \
  -dataShards 10 \
  -parityShards 3 \
  -apply \
  -source.grpc 127.0.0.1:18080 \
  -output.dir ./ec-output
```

Output artifacts:

- Per-shard JSONL files: `<vid>.shard.<index>.jsonl`
- Manifest: `<vid>.ec.manifest.json`
