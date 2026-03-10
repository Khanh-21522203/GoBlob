#!/usr/bin/env bash
set -euo pipefail

go test -bench=. -benchmem ./benchmark/... > /tmp/goblob_bench_new.txt
if command -v benchstat >/dev/null 2>&1; then
  benchstat benchmark/baseline.txt /tmp/goblob_bench_new.txt
else
  echo "benchstat not installed; raw results:"
  cat /tmp/goblob_bench_new.txt
fi
