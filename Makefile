# OTAP top-level Makefile
#
# This orchestrates the cross-language build (Go FLR + Rust OBG + simulated
# fabric) and the demo flows. Run targets here, not in subdirectories, when
# you want everything to stay in sync.
#
# Quick paths through the build:
#
#   make check           # cargo check + go vet — fast static-correctness sweep
#   make test            # cargo test + go test — full unit + integration suite
#   make demo-local      # the no-FPGA, no-FLR proof binary
#   make demo-fullstack  # spawns flrd, seeds, runs the live demo, cleans up
#   make golden          # regenerate cross-language golden vectors
#
# All targets are idempotent.

.PHONY: help all check test build clean \
        rust-check rust-test rust-build rust-clean \
        flr flr-build flr-test flr-clean flr-seed flrd \
        demo-local demo-modq demo-fullstack \
        golden verify-golden verify-golden-http \
        bench-bitweave bench-modq \
        format

# Pin the operator ID used across demo targets so the seed and the demo agree.
OPERATOR ?= op-alice
FLR_DATA ?= /tmp/otap-flr-data
FLR_HTTP ?= :8080
FLR_GRPC ?= :9090

help:
	@echo "OTAP integrated build targets"
	@echo ""
	@echo "  Status / sanity"
	@echo "    make check               # cargo check + go vet (incl. modq feature)"
	@echo "    make test                # full test suite (Rust + Go + bitweave fuzzer)"
	@echo ""
	@echo "  Build"
	@echo "    make rust-build          # cargo build --workspace --release"
	@echo "    make flr-build           # build flrd + flr-seed Go binaries"
	@echo "    make build               # both"
	@echo ""
	@echo "  Demo"
	@echo "    make demo-local          # pure-software OTAP demo (no FLR, no FPGA)"
	@echo "    make demo-fullstack      # live FLR + Rust drivers end-to-end"
	@echo "    make demo-modq           # MODQ loopback (no FPGA, no FLR)"
	@echo ""
	@echo "  Benchmarks"
	@echo "    make bench-bitweave      # Go bitweave conflict scanner ns/query"
	@echo "    make bench-modq          # Rust MODQ ring throughput + latency"
	@echo ""
	@echo "  Cross-language vectors"
	@echo "    make golden                          # regenerate testdata/golden_vectors.json"
	@echo "    make verify-golden                   # run Rust structural-equivalence checks (on-disk)"
	@echo "    make verify-golden-http              # cross-check against a live Workers FLR"
	@echo "      requires OTAP_FLR_URL=https://your-worker.workers.dev"
	@echo ""
	@echo "  Cleanup"
	@echo "    make clean               # remove all build artifacts and FLR data"

all: build

# ============================================================================
# Static checks
# ============================================================================

check: rust-check
	cd flr && go vet ./...

rust-check:
	cargo check --workspace --all-targets
	cargo check --workspace --all-targets --features modq

# ============================================================================
# Full test suite
# ============================================================================

test: rust-test flr-test

rust-test:
	cargo test --workspace --release

flr-test:
	cd flr && go test ./... -race

# Run the bitweave benchmarks (needs local Go toolchain)
bench-bitweave:
	cd flr && go test ./internal/bitweave -bench=. -benchmem -count=3

# Run the MODQ ring throughput benchmark (needs local Rust toolchain)
bench-modq:
	cargo bench -p otap-modq

# ============================================================================
# Build
# ============================================================================

build: rust-build flr-build

rust-build:
	cargo build --workspace --release

flr-build:
	$(MAKE) -C flr build

flr-clean:
	$(MAKE) -C flr clean

rust-clean:
	cargo clean

# ============================================================================
# Demos
# ============================================================================

# Pure software, no FLR, no network — the "we don't need an FPGA" demo.
demo-local: rust-build
	@echo "===> Running local (no-FLR) OTAP demo"
	cargo run --release -p otap-cli --bin otap-local

# MODQ loopback — exercises the PCIe ring protocol in shared memory.
demo-modq: rust-build
	@echo "===> Running MODQ loopback demo (no FPGA)"
	cargo run --release -p otap-cli --bin otap-modq-loopback

# Spawns flrd in the background, seeds it, runs the demo, cleans up.
demo-fullstack: build
	@echo "===> Starting flrd in background"
	@rm -rf $(FLR_DATA)
	@mkdir -p $(FLR_DATA)
	@./flr/bin/flrd -operator $(OPERATOR) -data $(FLR_DATA) -http $(FLR_HTTP) -grpc $(FLR_GRPC) >/tmp/flrd.log 2>&1 & echo $$! > /tmp/flrd.pid
	@sleep 2
	@echo "===> Seeding FLR with canonical schemas"
	@./flr/bin/flr-seed -url http://localhost$(FLR_HTTP) -operator $(OPERATOR)
	@echo "===> Running full-stack demo"
	@cargo run --release -p otap-cli --bin otap-fullstack -- \
	    --flr http://localhost$(FLR_HTTP) \
	    --operator $(OPERATOR) || (kill $$(cat /tmp/flrd.pid) 2>/dev/null; exit 1)
	@echo "===> Stopping flrd"
	@kill $$(cat /tmp/flrd.pid) 2>/dev/null && rm -f /tmp/flrd.pid /tmp/flrd.log || true
	@rm -rf $(FLR_DATA)

# ============================================================================
# Cross-language golden vectors
# ============================================================================

golden:
	$(MAKE) -C flr emit-golden-vectors
	@echo "===> Verifying from Rust side"
	cargo test -p otap-flr-client --test golden_vectors --release

verify-golden:
	$(MAKE) -C flr golden-vectors
	cargo test -p otap-flr-client --test golden_vectors --release

# Live cross-check against a deployed Cloudflare Workers FLR.
# Requires OTAP_FLR_URL to be set to the Worker URL.
#
#   make verify-golden-http OTAP_FLR_URL=https://otap-flr-worker.xxx.workers.dev
#
# This pulls /v1/golden_vectors over HTTP and validates the same structural
# rules as the on-disk vector test — including a determinism check that
# requires the Worker to emit byte-identical responses on consecutive calls
# (RFC 6979 deterministic ECDSA in @noble/curves).
verify-golden-http:
	@if [ -z "$$OTAP_FLR_URL" ]; then \
	  echo "ERROR: OTAP_FLR_URL not set. Example:"; \
	  echo "  make verify-golden-http OTAP_FLR_URL=https://your-worker.workers.dev"; \
	  exit 1; \
	fi
	OTAP_FLR_URL="$$OTAP_FLR_URL" \
	  cargo test -p otap-flr-client --test golden_vectors_http --release -- --nocapture

# ============================================================================
# Formatting
# ============================================================================

format:
	cargo fmt --all
	cd flr && go fmt ./...

# ============================================================================
# Cleanup
# ============================================================================

clean: rust-clean flr-clean
	@rm -rf $(FLR_DATA) /tmp/flrd.pid /tmp/flrd.log
