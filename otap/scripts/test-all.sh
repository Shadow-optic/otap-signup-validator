#!/bin/bash
set -euo pipefail

echo "=== OTAP Full Test Suite ==="
echo

# Rust tests
echo "→ Running Rust unit tests..."
cd rust
cargo test --all --release
echo

# Go tests  
echo "→ Running Go tests..."
cd ../go/bitweave
go test -v
echo

# Integration tests
echo "→ Running integration tests..."
cd ../../rust/integration
cargo test --release
echo

echo "✓ All tests passed!"
