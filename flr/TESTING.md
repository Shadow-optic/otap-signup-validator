# Federated Lambda Registry (FLR) - Testing Guide

## Table of Contents

1. [Testing Strategy](#1-testing-strategy)
2. [Test Structure](#2-test-structure)
3. [Unit Tests](#3-unit-tests)
4. [Integration Tests](#4-integration-tests)
5. [Test Fixtures](#5-test-fixtures)
6. [Running Tests](#6-running-tests)
7. [Test Utilities](#7-test-utilities)
8. [Coverage Report](#8-coverage-report)
9. [Performance/Benchmark Tests](#9-performancebenchmark-tests)
10. [Adding New Tests](#10-adding-new-tests)
11. [Continuous Integration](#11-continuous-integration)

---

## 1. Testing Strategy

### Philosophy

The FLR testing strategy follows a **balanced approach** that prioritizes correctness of the distributed consensus and cryptographic primitives while maintaining fast feedback loops for developers. Tests are designed to exercise the critical path of lease allocation, conflict detection, and cross-operator federation synchronization.

Core principles:

- **Fail fast on cryptographic errors**: Signature verification, Merkle tree integrity, and hash chain consistency are the most safety-critical operations and receive the highest test density.
- **Mock external dependencies**: All tests use in-memory or temporary-file storage to avoid infrastructure coupling.
- **Deterministic execution**: Tests never depend on wall-clock timing or network availability (except where explicitly testing timeout behavior).
- **Concurrency validation**: Thread-safety tests verify that the registry engine, federation manager, and translation manager are safe under concurrent access.

### Test Pyramid

```
                    /\                    
                   /  \   207 tests
                  / E2E \   (3 integration suites)
                 /--------\
                /          \                    
               / Integration \   gRPC round-trip,
              /   (3 suites)   \  HTTP federation
             /------------------\  multi-operator sync
            /                    \                    
           /      Unit Tests       \   Crypto, Registry,
          /      (10 packages)       \  Store, Federation,
         /        (207 tests)         \ XLAT, Audit, API
        /------------------------------\
       /         Mock Stores            \ InMemoryStore, MockStore,
      /     (MemoryStore, httptest)     \  httptest.Server, bufconn
     /------------------------------------\
```

### Coverage Targets

| Category | Target Coverage |
|----------|----------------|
| Cryptographic operations | >= 95% |
| Registry engine (CRUD + conflict) | >= 90% |
| Persistence layer (BadgerDB) | >= 85% |
| Federation protocol | >= 80% |
| Audit & compliance | >= 85% |
| API / gRPC handlers | >= 75% |
| Overall codebase | >= 80% |

---

## 2. Test Structure

### Directory Layout

```
flr/
|-- internal/
|   |-- crypto/
|   |   |-- engine_test.go           # Signature, Merkle tree, token tests
|   |-- registry/
|   |   |-- engine_test.go           # Lease CRUD, conflict, commitment tests
|   |   |-- badger_store_test.go     # BadgerDB persistence tests
|   |-- federation/
|   |   |-- manager_test.go          # Sync, gossip, conflict detection tests
|   |   |-- client_test.go           # HTTP federation client tests
|   |-- xlat/
|   |   |-- manager_test.go          # AWG tables, translation tests
|   |-- audit/
|   |   |-- auditor_test.go          # Hash chain, audit report tests
|   |   |-- compliance_test.go       # ITU compliance, DWDM grid tests
|   |-- api/
|   |   |-- server_test.go           # gRPC handler tests
|-- contracts/
|   |-- chaincode_test.go            # Fabric chaincode unit tests
|-- test/
|   |-- helper.go                    # Shared test utilities
|   |-- integration/
|   |   |-- lease_lifecycle_test.go  # End-to-end lease lifecycle
|   |   |-- merkle_test.go           # Merkle commitment flow
|   |   |-- federation_sync_test.go  # Multi-operator synchronization
|   |-- fixtures/
|   |   |-- leases.json              # 18 sample lease records
|   |   |-- operators.json           # 3 sample operator records
|   |   |-- keys/
|   |   |   |-- generate.go          # Key-pair generator for fixtures
```

### Test File Inventory

| File | Package | Test Functions | Focus |
|------|---------|---------------|-------|
| `internal/crypto/engine_test.go` | `crypto` | 24+ | ECDSA signatures, Merkle trees, lease tokens, proofs of invalidity |
| `internal/registry/engine_test.go` | `registry` | 15+ | Lease CRUD, conflict detection, commitments, expiry |
| `internal/registry/badger_store_test.go` | `registry` | 16+ | BadgerDB lease/endpoint/operator/commitment/audit storage |
| `internal/federation/manager_test.go` | `federation` | 25+ | Operator registry, gossip, conflict detection, PoI handling |
| `internal/federation/client_test.go` | `federation` | 19+ | HTTP client for commitments, leases, proofs, streaming |
| `internal/xlat/manager_test.go` | `xlat` | 22+ | Translation CRUD, AWG tables, validation, Merkle root computation |
| `internal/audit/auditor_test.go` | `audit` | 8+ | Audit logging, ITU compliance, Merkle consistency, reports |
| `internal/audit/compliance_test.go` | `audit` | 10+ | DWDM grid math, band validation, frequency/wavelength conversion |
| `internal/api/server_test.go` | `api` | 5+ | gRPC CreateLease, error handling, bufconn transport |
| `contracts/chaincode_test.go` | `main` | 4+ | Fabric InitLedger, RegisterOperator, mock chaincode stub |
| `test/integration/lease_lifecycle_test.go` | `integration` | 4 | Full lease create -> get -> renew -> revoke -> expire cycle |
| `test/integration/merkle_test.go` | `integration` | 3 | Merkle tree build -> sign -> verify proof -> tamper detection |
| `test/integration/federation_sync_test.go` | `integration` | 4 | Cross-operator sync, conflict detection, operator registration |

---

## 3. Unit Tests

### 3.1 Crypto Engine Tests (`internal/crypto/engine_test.go`)

The crypto engine tests validate the entire cryptographic foundation of FLR: ECDSA P-256 signatures, SHA3-256 hashing, Merkle tree construction, and tamper-proof lease tokens.

**Key test groups:**

| Group | Tests | Description |
|-------|-------|-------------|
| `TestNewEngine` | 5 cases | Validates engine creation with valid keys, empty operator ID, empty key, invalid PEM, wrong curve (P-384) |
| `TestGenerateKeyPair` | 1 test | Verifies P-256 key generation produces valid PEM-encoded private and public keys |
| `TestEngine_Sign` | 3 cases | Signs valid data, rejects empty data, handles large (1KB) payloads |
| `TestVerify` | 7 cases | Verifies correct signatures, rejects wrong key/empty inputs/tampered data/wrong signatures |
| `TestGenerateLeaseToken` | 2 tests | Creates tokens with correct fields (v1, 32-byte nonce, 64-byte signature) |
| `TestValidateLeaseToken` | 7 sub-tests | Validates tokens, rejects expired/tampered/wrong-key tokens |
| `TestGetLeaseHash` | 4 sub-tests | Verifies deterministic SHA3-256 hashing, different leases yield different hashes |
| `TestBuildMerkleTree` | 7 sub-tests | Empty tree, single lease, power-of-2, odd-count padding, deterministic sorting |
| `TestSignMerkleCommitment` | 3 sub-tests | Signs valid commitments, rejects empty/nil root hashes |
| `TestVerifyMerkleCommitment` | 6 sub-tests | Verifies signed commitments, rejects nil/tampered/wrong-key inputs |
| `TestVerifyMerkleProof` | 7 sub-tests | Validates inclusion proofs, rejects tampered proofs/wrong roots/empty inputs |
| `TestGenerateProofOfInvalidity` | 5 sub-tests | Generates PoI for double-allocation, expired leases; validates inputs |
| `TestVerifyProofOfInvalidity` | 5 sub-tests | Verifies PoI integrity, rejects nil/missing-commitment/wrong-root cases |
| `TestMarshalSignature` / `TestUnmarshalSignature` | 2 tests | Round-trip (r,s) <-> 64-byte serialization |
| `TestParsePublicKey` | 4 sub-tests | PEM and raw DER parsing, empty input handling |
| `TestGenerateLeaseID` | 1 test | UUID generation uniqueness and format validation |
| `TestCanonicalJSONDeterminism` | 1 test | Verifies canonical JSON produces identical hashes |

### 3.2 Registry Engine Tests (`internal/registry/engine_test.go`)

Tests the lease allocation, conflict detection, renewal, revocation, expiry, and Merkle commitment lifecycle using an in-memory mock store.

**Key test groups:**

| Group | Tests | Description |
|-------|-------|-------------|
| `TestEngine_AllocateLease` | 6 sub-tests | Allocates leases, validates inputs (nil WL, empty EP, zero/negative duration), rejects double-allocation |
| `TestEngine_RenewLease` | 5 sub-tests | Renews active leases, rejects non-existent/empty/revoked leases, validates extension |
| `TestEngine_RevokeLease` | 4 sub-tests | Revokes active leases, handles already-revoked/non-existent cases |
| `TestEngine_CheckConflict` | 4 sub-tests | Detects wavelength conflicts, respects excluded lease IDs |
| `TestEngine_BuildMerkleTree` | 2 sub-tests | Builds Merkle trees over stored leases |
| `TestEngine_CommitMerkleTree` | 3 sub-tests | Commits empty and populated trees, verifies block height increment |
| `TestEngine_GetLatestCommitment` | 1 test | Retrieves latest commitment after multiple commits |
| `TestEngine_VerifyLease` | 3 sub-tests | Verifies lease tokens, rejects nil/non-existent tokens |
| `TestEngine_ExpireLeases` | 3 sub-tests | Expires overdue leases, handles no-matches and active leases |
| `TestEngine_GetBlockHeight` | 1 test | Block height starts at 0 and increments on commit |
| `TestEngine_ConcurrentAllocations` | 1 test | 10 concurrent allocations (thread safety) |
| `TestEngine_ConcurrentCommits` | 1 test | 3 concurrent commits (mutex serialization) |

### 3.3 BadgerDB Store Tests (`internal/registry/badger_store_test.go`)

Validates the production BadgerDB persistence layer in temporary directories (cleaned up automatically via `t.TempDir()` and `t.Cleanup()`).

**Key test groups:**

| Group | Tests | Description |
|-------|-------|-------------|
| `TestBadgerStore_CreateLease` | 4 sub-tests | Create, duplicate rejection, nil validation, empty ID |
| `TestBadgerStore_GetLease` | 3 sub-tests | Get existing, non-existent, empty ID |
| `TestBadgerStore_UpdateLease` | 4 sub-tests | Update existing, non-existent, nil, empty ID |
| `TestBadgerStore_DeleteLease` | 3 sub-tests | Delete existing, non-existent (no-op), empty ID |
| `TestBadgerStore_ListLeases` | 5 sub-tests | List all, filter by operator/status/both, no matches |
| `TestBadgerStore_*Endpoint*` | 6 tests | CRUD + listing for endpoints |
| `TestBadgerStore_*Operator*` | 3 tests | Create, get, list operators |
| `TestBadgerStore_*Commitment*` | 6 tests | Save, get, get-latest for Merkle commitments |
| `TestBadgerStore_*AuditLog*` | 3 tests | Append audit entries, retrieve by time range |
| `TestBadgerStore_StoreInterface` | 1 test | Compile-time interface compliance check |

### 3.4 Federation Tests

#### Federation Manager (`internal/federation/manager_test.go`)

| Group | Tests | Description |
|-------|-------|-------------|
| `TestManager_RegisterOperator` | 5 sub-tests | Register, nil input, missing ID/endpoint, duplicate handling |
| `TestManager_*Operator*` | 5 tests | Get, list, remove operators; self-removal protection |
| `TestManager_DetectConflicts_*` | 4 sub-tests | Double-allocation detection, no overlap, no peers, offline peer |
| `TestManager_HandleProofOfInvalidity_*` | 6 sub-tests | Handle PoI for double-allocation, expired lease, invalid sig, unknown type |
| `TestManager_PushCommitmentToAll_*` | 4 tests | Push to peers, nil input, no peers, inactive peer handling |
| `TestManager_GetTranslationTable_*` | 2 tests | Fetch remote translation tables, not-found handling |
| `TestManager_StartAndStopGossip_*` | 2 tests | Gossip lifecycle, multiple start/stop cycles |
| `TestManager_SyncWithOperator_*` | 2 tests | Sync remote operator data, not-found handling |
| `TestManager_ConcurrentAccess` | 1 test | 100 concurrent get/list/register operations |
| `TestManager_ConcurrentRemove` | 1 test | 10 concurrent operator removals |

#### Federation Client (`internal/federation/client_test.go`)

Tests the HTTP federation client against local `httptest.Server` instances:

| Group | Tests | Description |
|-------|-------|-------------|
| `TestClient_GetCommitment*` | 3 tests | Fetch commitments, 404 handling, 500 handling |
| `TestClient_GetLeases*` | 2 tests | Fetch leases, empty response |
| `TestClient_PushCommitment*` | 3 tests | Push commitments, 500 handling, nil input |
| `TestClient_SubmitProofOfInvalidity*` | 2 tests | Submit PoI, 500 handling |
| `TestClient_RegisterOperator*` | 2 tests | Register operator, 409 conflict handling |
| `TestClient_GetOperator*` | 2 tests | Get operator, 404 handling |
| `TestClient_GetTranslationTable*` | 2 tests | Fetch translation tables, 500 handling |
| `TestClient_HealthCheck*` | 3 tests | Healthy check, unhealthy, unreachable host |
| `TestClient_StreamUpdates*` | 2 tests | SSE stream parsing, 403 error handling |

### 3.5 Translation Tests (`internal/xlat/manager_test.go`)

Tests the AWG wavelength translation and routing table generation:

| Group | Tests | Description |
|-------|-------|-------------|
| `TestManager_CreateTranslation*` | 6 sub-tests | Create translation, validate inputs (missing ops, nil WL, zero duration, invalid port, conflict detection) |
| `TestManager_GetTranslation*` | 2 tests | Get translation, empty ID rejection |
| `TestManager_ListTranslations*` | 2 tests | List all, filtered listing |
| `TestManager_GenerateAWGTable*` | 3 tests | Generate empty/populated tables, validate inputs |
| `TestManager_ValidateTranslation*` | 6 sub-tests | Validate entries: valid, nil, missing WL, invalid port, expired, backward time, conflicting lease |
| `TestManager_ExportForAWG*` | 3 tests | JSON export, empty inputs, round-trip JSON serialization |
| `TestWavelengthsEqual*` | 3 tests | Equality check, different lambda, nil handling |
| `TestComputeMerkleRoot*` | 4 tests | Empty, single, three entries, determinism |
| `TestAWGRouting*_JSONRoundTrip` | 2 tests | Entry and table JSON serialization |
| `TestManager_CreateTranslation_ManyTranslations` | 1 test | Bulk creation of 50 translations |
| `TestManager_ConcurrentCreateTranslation` | 1 test | 20 concurrent translations |

### 3.6 Audit Tests

#### Auditor (`internal/audit/auditor_test.go`)

| Group | Tests | Description |
|-------|-------|-------------|
| `TestNewAuditor` | 1 test | Auditor initialization |
| `TestLogOperation` | 1 test | Appends audit log entries with SHA3-256 hashes |
| `TestValidateITUCompliance` | 5 cases | Validates C-band wavelengths, grid spacing, channel numbers |
| `TestValidateDWDMGrid` | 2 sub-tests | On-grid and off-grid detection |
| `TestCheckLeaseChainIntegrity` | 2 sub-tests | Empty registry, active lease with/without token hash |
| `TestVerifyMerkleConsistency` | 2 sub-tests | No operators, consistent/inconsistent commitment counts |
| `TestGenerateAuditReport` | 4 sub-tests | Empty report, populated report, expired-but-active detection, ITU violation |
| `TestGenerateAuditReportWithCommitments` | 1 test | Report with commitments and audit log entries |

#### Compliance (`internal/audit/compliance_test.go`)

| Group | Tests | Description |
|-------|-------|-------------|
| `TestFrequencyFromWavelength` | 4 cases | nm <-> THz conversion at known wavelengths |
| `TestWavelengthFromFrequency` | 3 cases | THz <-> nm round-trip |
| `TestRoundTripConversion` | 5 wavelengths | Wavelength -> frequency -> wavelength identity |
| `TestIsOnGrid` | 6 cases | 50GHz/25GHz/100GHz grid alignment |
| `TestChannelNumberFromWavelength` | 6 cases | ITU-T channel number computation |
| `TestWavelengthFromChannelNumber` | 5 cases | Round-trip channel <-> wavelength |
| `TestValidateBand` | 13 cases | C-band, L-band, S-band boundary validation |
| `TestGridSpacings` | 1 test | Returns [12.5, 25.0, 50.0, 100.0] GHz |
| `TestGridInfo` | 1 test | ITU-T G.694.1 spec compliance |
| `TestSpeedOfLightConstant` | 1 test | c ~ 2.998e17 nm/s |
| `TestKnownITUWavelengths` | 1 test | Channel 0 = 1552.524 nm at 193.1 THz |

---

## 4. Integration Tests

Integration tests live in `test/integration/` and exercise end-to-end workflows across multiple subsystems without mocking the store. They use the shared `MemoryStore` from `test/helper.go`.

### 4.1 End-to-End Lease Lifecycle (`test/integration/lease_lifecycle_test.go`)

```
TestLeaseLifecycle
  Step 1: Create lease (store.CreateLease)
  Step 2: Get lease (store.GetLease) -> verify status, wavelength
  Step 3: Verify data integrity (SHA-256 hash of key fields)
  Step 4: Renew lease (extend EndTime, store.UpdateLease)
  Step 5: Revoke lease (set Status=REVOKED, store.UpdateLease)

TestDoubleAllocationPrevention
  - First allocation succeeds, no conflict detected
  - Second allocation on same wavelength detected
  - Count overlapping leases on same wavelength

TestLeaseExpiry
  - Create expired lease (EndTime in past, Status still ACTIVE)
  - Create valid lease (EndTime in future)
  - Scan all leases, mark expired ones, verify active untouched

TestLeaseSerialization
  - JSON marshal/unmarshal round-trip preserves all fields
```

### 4.2 Merkle Commitment Flow (`test/integration/merkle_test.go`)

```
TestMerkleCommitment
  Step 1: Build Merkle tree from 5 lease IDs -> root hash
  Step 2: Sign root with ECDSA P-256 key
  Step 3: Store MerkleCommitment in store
  Step 4: Retrieve commitment via GetLatestCommitment
  Step 5: Verify inclusion proof for each lease

TestMerkleProofVerification (7 sub-tests)
  - Single leaf (proof is empty, leaf == root)
  - Two, four, five (odd) leaves
  - Tampered proof fails verification
  - Wrong leaf hash fails
  - Deterministic root hashing
  - Sorted input produces same root regardless of order

TestMerkleCommitmentSignature
  - ECDSA signature on root hash, 64-byte format
```

### 4.3 Federation Synchronization (`test/integration/federation_sync_test.go`)

```
TestFederationSync
  - Create two operators (A, B) with cross-registered views
  - A creates 2 leases, B creates 1 lease
  - Simulate A pushing leases to B
  - Exchange Merkle commitments
  - Verify B has 3 leases total and A's commitment

TestConflictDetection
  - 3 operators (A, B, C)
  - A and B allocate the SAME wavelength (1550.12 nm)
  - C allocates a different wavelength
  - Federation-wide scan detects exactly 1 conflict
  - Generate ProofOfInvalidity with Merkle proof

TestFederationOperatorRegistration
  - Register 3 operators, verify list and individual retrieval

TestCrossOperatorLeaseLookup
  - Create 5 leases for Operator A, 3 for Operator B
  - Filter by operator ID, verify counts
```

---

## 5. Test Fixtures

### `test/fixtures/leases.json`

Contains 18 sample lease records spanning three operators:

| Operator | Count | Wavelengths | Status Mix |
|----------|-------|------------|------------|
| Verizon (`op-verizon-001`) | 7 | 1530.33 - 1550.52 nm, C-band, 50GHz grid | 6 Active, 1 Expired, 1 Revoked |
| Lumen (`op-lumen-002`) | 6 | 1530.73 - 1552.52 nm, C-band, 50GHz grid | 5 Active, 1 Expired |
| Cogent (`op-cogent-003`) | 7 | 1531.12 - 1561.42 nm, C-band, 50GHz grid | 5 Active, 1 Expired, 1 Revoked |

Each lease includes a token hash and parent hash forming a chain-of-custody trail.

### `test/fixtures/operators.json`

Three federation operators with ECDSA P-256 public keys:

| ID | Name | Endpoint | Status |
|----|------|----------|--------|
| `op-verizon-001` | Verizon Optical Network | `https://flr.verizon.com:9443` | Active |
| `op-lumen-002` | Lumen Global Fiber | `https://flr.lumen.com:9443` | Active |
| `op-cogent-003` | Cogent Communications | `https://flr.cogentco.com:9443` | Active |

### `test/fixtures/keys/generate.go`

A utility program (`//go:build ignore`) that generates fresh ECDSA P-256 key pairs for the three fixture operators. Run with:

```bash
go run test/fixtures/keys/generate.go
```

Output: `test/fixtures/keys/{op-id}.pem` and `test/fixtures/keys/{op-id}.pub`.

---

## 6. Running Tests

### All Tests with Coverage

```bash
make test
```

Runs all tests with race detection and produces `coverage.out`:

```
go test -v -race -coverprofile=coverage.out ./internal/... ./pkg/... ./test/...
```

### Unit Tests Only

```bash
make unit-test
```

Runs unit tests across all internal packages:

```
go test -v -race ./internal/crypto/... ./internal/registry/... \
    ./internal/federation/... ./internal/xlat/... \
    ./internal/api/... ./internal/audit/...
```

### Integration Tests Only

```bash
make integration-test
```

Runs integration suites:

```
go test -v -race ./test/integration/...
```

### Individual Package Tests

```bash
go test -v ./internal/crypto/...
go test -v ./internal/registry/...
go test -v ./internal/federation/...
go test -v ./internal/xlat/...
go test -v ./internal/audit/...
go test -v ./test/integration/...
```

### HTML Coverage Report

```bash
make coverage
```

Generates `coverage.html` for visual inspection.

### Running Specific Tests

```bash
# Run a specific test function
go test -v -run TestBuildMerkleTree ./internal/crypto/...

# Run a sub-test by name
go test -v -run TestBuildMerkleTree/deterministic ./internal/crypto/...

# Run all tests in a file
go test -v ./internal/registry/engine_test.go ./internal/registry/engine.go

# Run with verbose output and race detection
go test -v -race ./internal/...
```

---

## 7. Test Utilities

### Shared Memory Store (`test/helper.go`)

The `MemoryStore` type implements the full `registry.Store` interface in memory. It is the backbone of integration tests and many unit tests.

```go
store := testutil.NewMemoryStore()
```

**Provided helpers:**

| Function | Purpose |
|----------|---------|
| `NewMemoryStore()` | Creates a fresh in-memory store |
| `GenerateTestKeyPair() (*ecdsa.PrivateKey, []byte, []byte, error)` | P-256 key pair for testing |
| `CreateTestLease(operatorID, endpointID string, wl *Wavelength) *Lease` | Creates a lease with timestamps |
| `CreateTestOperator(id, name string) *Operator` | Creates an operator with key pair |
| `MustParseTime(string) time.Time` | RFC3339 time parser (panics on error) |
| `Wavelength1550() *Wavelength` | 1550.12 nm, ch 4, C-band, 50 GHz |
| `Wavelength1530() *Wavelength` | 1530.33 nm, ch -34, C-band, 50 GHz |
| `Wavelength1560() *Wavelength` | 1560.61 nm, ch 21, C-band, 50 GHz |

### Mock Store Patterns

Each internal package defines its own mock store when it needs specialized behavior:

- **`internal/registry/engine_test.go`**: `inMemoryStore` -- full `registry.Store` implementation with `sync.RWMutex`, supports filtering, audit log, and commitment tracking.
- **`internal/federation/manager_test.go`**: `mockStore` -- configurable store with `listLeasesFn` hook for controlling query responses during conflict detection.
- **`internal/federation/manager_test.go`**: `testPeer` -- `httptest.Server`-based mock peer with configurable responses for `/v1/leases`, `/v1/commitments`, `/v1/operators`, `/v1/invalidity`, `/v1/translations`.
- **`internal/xlat/manager_test.go`**: `mockLeaseStore` -- minimal `registry.Store` implementation.
- **`internal/audit/auditor_test.go`**: `mockStore` -- simple map-backed store.

### HTTP Test Servers

Federation client tests use `httptest.Server` to simulate remote operators:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)
}))
defer server.Close()
```

### gRPC Test Transport

API server tests use `bufconn` for in-process gRPC:

```go
lis := bufconn.Listen(1024 * 1024)
grpcSrv := grpc.NewServer()
flrv1.RegisterFederatedRegistryServer(grpcSrv, apiServer)
// ... dial via bufconn context dialer
```

---

## 8. Coverage Report

### Expected Coverage by Module

| Module | Files | Tests | Lines (est.) | Expected Coverage |
|--------|-------|-------|-------------|-------------------|
| `internal/crypto` | `engine.go`, `hash.go` | 24+ | ~400 | 95% |
| `internal/registry` | `engine.go`, `badger_store.go`, `store.go` | 31+ | ~600 | 90% |
| `internal/federation` | `manager.go`, `client.go` | 44+ | ~800 | 80% |
| `internal/xlat` | `manager.go` | 22+ | ~400 | 80% |
| `internal/audit` | `auditor.go`, `compliance.go` | 18+ | ~350 | 85% |
| `internal/api` | `server.go`, `handlers.go` | 5+ | ~300 | 75% |
| `test/integration` | 3 suites | 11 scenarios | -- | -- |

### Coverage Summary Command

```bash
# Terminal summary
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1

# Per-package breakdown
go test -cover ./... | grep -E "coverage|ok|FAIL"

# Generate HTML report
go tool cover -html=coverage.out -o coverage.html
```

---

## 9. Performance/Benchmark Tests

Three benchmark functions in `internal/crypto/engine_test.go`:

| Benchmark | Input Size | Measures |
|-----------|-----------|----------|
| `BenchmarkGetLeaseHash` | Single lease | SHA3-256 hash computation latency |
| `BenchmarkBuildMerkleTree` | 100 leases | Merkle tree construction throughput |
| `BenchmarkGenerateLeaseToken` | Single lease | ECDSA sign + token assembly latency |

### Running Benchmarks

```bash
# Run all benchmarks
go test -bench=. -benchmem ./internal/crypto/...

# Run specific benchmark with detailed memory stats
go test -bench=BenchmarkBuildMerkleTree -benchmem -benchtime=5s ./internal/crypto/...

# Compare benchmark results (requires benchstat)
go test -bench=. -count=5 ./internal/crypto/... | tee old.txt
# ... make changes ...
go test -bench=. -count=5 ./internal/crypto/... | tee new.txt
benchstat old.txt new.txt
```

---

## 10. Adding New Tests

### Naming Conventions

- Test files: `*_test.go` (co-located with source)
- Integration test files: `test/integration/*_test.go`
- Test functions: `Test{FunctionName}_{Scenario}` or `Test{FunctionName}` with `t.Run()` sub-tests
- Benchmark functions: `Benchmark{FunctionName}`
- Mock types: Prefix with package-specific name (e.g., `mockStore`, `testPeer`)

### Pattern: Table-Driven Tests

```go
func TestNewFunction(t *testing.T) {
    tests := []struct {
        name        string
        input       string
        wantErr     bool
        errContains string
    }{
        {
            name:   "valid input",
            input:  "valid",
            wantErr: false,
        },
        {
            name:        "empty input",
            input:       "",
            wantErr:     true,
            errContains: "cannot be empty",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := NewFunction(tt.input)
            if tt.wantErr {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.errContains)
                return
            }
            require.NoError(t, err)
            assert.NotNil(t, result)
        })
    }
}
```

### Pattern: Using Shared Test Helpers

```go
import testutil "github.com/otap/flr/test"

func TestMyFeature(t *testing.T) {
    store := testutil.NewMemoryStore()
    op := testutil.CreateTestOperator("op-test", "Test Op")
    store.CreateOperator(op)

    wl := testutil.Wavelength1550()
    lease := testutil.CreateTestLease(op.ID, "ep-001", wl)
    store.CreateLease(lease)

    // ... test your feature
}
```

### Pattern: Mock HTTP Peers

```go
func TestFederationCall(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        assert.Equal(t, "/v1/leases", r.URL.Path)
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode([]*models.Lease{lease})
    }))
    defer server.Close()

    client := federation.NewClient(5 * time.Second)
    leases, err := client.GetLeases(server.URL, registry.LeaseFilter{})
    require.NoError(t, err)
    assert.Len(t, leases, 1)
}
```

### Guidelines

1. **Always use `t.Helper()`** in helper functions to get correct failure line numbers.
2. **Always clean up resources**: Use `t.TempDir()` for files, `t.Cleanup()` for closables, `defer server.Close()` for HTTP servers.
3. **Use `require` for prerequisites** and `assert` for actual checks.
4. **Test error messages**: Use `assert.Contains(t, err.Error(), "expected substring")` rather than exact string matching.
5. **Run with `-race`**: All tests should pass with the race detector enabled.
6. **Keep tests deterministic**: Never depend on wall-clock time; use fixed timestamps where possible.
7. **Document concurrent tests**: Always use `sync.WaitGroup` and `require` inside goroutines, or send results back to the main test goroutine.

---

## 11. Continuous Integration

### Suggested CI Pipeline (GitHub Actions)

```yaml
name: FLR CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6

  unit-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - name: Unit Tests
        run: go test -v -race ./internal/... ./pkg/...
      - name: Coverage
        run: go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

  integration-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - name: Integration Tests
        run: go test -v -race -timeout 5m ./test/integration/...

  build:
    runs-on: ubuntu-latest
    needs: [lint, unit-tests]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - name: Build
        run: go build -o bin/flr ./cmd/flr
      - name: Vet
        run: go vet ./...
```

### Pre-Commit Checklist

Before committing, run locally:

```bash
make fmt          # Format all code
make vet          # Static analysis
make lint         # Linter (golangci-lint)
make unit-test    # All unit tests with race detector
make integration-test  # All integration tests
```

### Key CI Considerations

1. **Race detector timeout**: The `-race` flag can slow tests by 2-10x. Set adequate timeout (`-timeout 10m`).
2. **BadgerDB cleanup**: BadgerDB tests use `t.TempDir()` for automatic cleanup. Ensure CI runners have sufficient temp space.
3. **Go version**: FLR requires Go 1.22+ (uses `slog`, `iter` packages).
4. **Coverage gating**: Consider blocking PRs that reduce coverage below the module thresholds in Section 8.
5. **Flaky test detection**: Run integration tests with `-count=3` in CI to catch flakiness.

---

## Appendix: Quick Reference

| Task | Command |
|------|---------|
| Run all tests | `make test` |
| Run unit tests | `make unit-test` |
| Run integration tests | `make integration-test` |
| Run with coverage | `make coverage` |
| Run benchmarks | `go test -bench=. ./internal/crypto/...` |
| Run single test | `go test -v -run TestName ./pkg/...` |
| Generate test keys | `go run test/fixtures/keys/generate.go` |
| Format code | `make fmt` |
| Run linter | `make lint` |
| Build binary | `make build` |
| Clean artifacts | `make clean` |
