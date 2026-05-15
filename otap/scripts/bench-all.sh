#!/bin/bash
set -euo pipefail

echo "=== OTAP Benchmark Suite ==="
echo

# MODQ benchmarks
echo "→ MODQ throughput..."
cd rust/modq
cargo bench --bench ring_throughput
echo

# BitWeave benchmarks
echo "→ BitWeave conflict detection..."
cd ../../go/bitweave
go test -bench=. -benchtime=3s
echo

# D3 crypto benchmarks
echo "→ D3 crypto performance..."
cd ../../rust/d3
cargo bench
echo

echo "✓ Benchmarks complete!"
