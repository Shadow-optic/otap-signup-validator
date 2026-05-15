# FLR: Federated Lambda Registry

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![gRPC](https://img.shields.io/badge/gRPC-1.71%2B-orange)](https://grpc.io/)
[![Protocol Buffers](https://img.shields.io/badge/Protobuf-v3-purple)](https://protobuf.dev/)
[![Docker](https://img.shields.io/badge/Docker-ready-blue)](docker-compose.yml)

> **A cryptographically secured, distributed wavelength registry for multi-operator OTAP networks implementing tamper-evident Merkle-tree commitments and time-bound lease tokenization.**

---

## Abstract

The **Federated Lambda Registry (FLR)** is a production-grade distributed system designed to manage wavelength (lambda) assignments across multiple independent OTAP (Optical Transport Access Platform) network operators. Built on a foundation of modern cryptography and distributed systems principles, FLR addresses the fundamental challenge of resource coordination in multi-operator optical networks: how can competing operators share a finite wavelength spectrum while maintaining cryptographic verifiability, preventing double-allocation, and ensuring end-to-end optical path continuity across administrative boundaries?

FLR serves as the trust anchor for the OTAP federation layer. Each participating operator runs an independent FLR node that maintains its own local registry of wavelength allocations, endpoint registrations, and cross-operator translation tables. The system's key innovation lies in its use of **Merkle-tree commitments** combined with **ECDSA digital signatures** to create tamper-evident, publicly verifiable snapshots of registry state. When an operator commits a new Merkle root, that commitment is cryptographically signed and broadcast to all peers, creating an immutable audit trail of registry mutations. Any attempt to retroactively modify a lease record would invalidate the Merkle root, making tampering immediately detectable by any peer operator.

The second major innovation is **tamper-evident lease tokenization**. Every wavelength lease issued by an FLR node is represented as a cryptographically signed token containing the lease parameters (wavelength, endpoint, validity period, operator identity), a unique nonce, and an ECDSA signature. These tokens serve as portable proofs of allocation that can be verified offline by any party holding the issuing operator's public key. Combined with cross-operator federation via gossip-based synchronization and proof-of-invalidity mechanisms for conflict detection, FLR provides a complete, cryptographically secured resource management layer for federated optical networks — all while remaining compatible with passive Arrayed Waveguide Grating (AWG) junctions that require zero electrical processing at the optical layer.

---

## Features

- **Merkle-Tree Registry Commitments** — Every registry snapshot is hashed into a Merkle tree; the signed root is published as a tamper-evident commitment visible to all federation peers
- **Cryptographic Lease Tokenization** — All wavelength leases are issued as ECDSA-signed tokens with embedded nonces, enabling offline verification and preventing forgery
- **Cross-Operator Federation** — Gossip-based synchronization protocol with automatic peer discovery, commitment broadcasting, and conflict detection across administrative boundaries
- **Double-Allocation Detection** — Cryptographic proof-of-invalidity (PoI) mechanism for detecting and reporting wavelength conflicts between competing operators
- **ITU-T Grid Compliance** — Automatic validation of all wavelength assignments against ITU-T C-band/L-band/S-band grid standards with configurable spacing (25 GHz, 50 GHz, 100 GHz)
- **Audit-Grade Logging** — Immutable hash-chained audit log of all registry mutations with structured JSON output and compliance report generation
- **Passive AWG Compatibility** — Pre-negotiated cross-domain lambda translation tables exportable in vendor-neutral JSON for passive optical junctions
- **Dual-Protocol API** — Both gRPC (for high-performance internal communication) and REST HTTP/JSON (for external integration) endpoints via grpc-gateway
- **Embedded Storage** — Zero external database dependencies; uses BadgerDB as the embedded key-value store for lease, endpoint, and commitment persistence
- **mTLS Security** — Mutual TLS authentication for all federation traffic with per-operator certificate pinning
- **Hyperledger Fabric Chaincode** — Optional blockchain anchoring of Merkle commitments for enhanced trust distribution
- **Docker-Ready Deployment** — Multi-stage Dockerfile and docker-compose configuration for 3-node federation testing with optional Prometheus/Grafana monitoring

---

## Architecture Overview

FLR is organized into seven internal modules, each responsible for a distinct subsystem. The architecture follows a layered design with clean interfaces between the cryptographic engine, registry storage, federation layer, API surface, and compliance auditing.

```
+===================================================================================+
|                              FLR NODE ARCHITECTURE                                |
+===================================================================================+
|                                                                                   |
|  +-------------------+  +-------------------+  +-------------------+             |
|  |   cmd/flr (CLI)   |  |  gRPC Gateway     |  |  REST HTTP Server |             |
|  |   Cobra Commands  |  |  (grpc-gateway)   |  |  (JSON via :8080) |             |
|  +---------+---------+  +---------+---------+  +---------+---------+             |
|            |                      |                      |                       |
+------------|----------------------|----------------------|-----------------------+
|            v                      v                      v                       |
|  +=======================================================================+       |
|  |                    internal/api — API Server Layer                      |       |
|  |   Auth Interceptor | Rate Limiter | Recovery | Logging Interceptor      |       |
|  +===========================+===========================================+       |
|                              |                                                    |
|            +-----------------+-----------------+                                  |
|            v                 v                 v                                  |
|  +-------------------+ +-------------+ +------------------------+               |
|  | internal/registry | |internal/xlat| |  internal/federation    |               |
|  | — Registry Engine | | — Translation| | — Federation Manager   |               |
|  | — BadgerDB Store  | |   Table Mgr  | | — Gossip Protocol     |               |
|  | — Lease Lifecycle | | — AWG Export | | — Conflict Detection  |               |
|  | — Merkle Builder  | |              | | — PoI Handler         |               |
|  +---------+---------+ +------+------+ +-----------+------------+               |
|            |                  |                     |                            |
|            v                  v                     v                            |
|  +=======================================================================+       |
|  |                    internal/crypto — Cryptographic Engine               |       |
|  |   ECDSA P-256 | SHA3-256 | Merkle Trees | HMAC-SHA256 | Token Engine    |       |
|  +===========================+===========================================+       |
|                              |                                                    |
|            +-----------------+-----------------+                                  |
|            v                 v                 v                                  |
|  +-------------------+ +-------------+ +------------------------+               |
|  | internal/models   | | BadgerDB v4 | |  internal/audit        |               |
|  | — Core Types      | | — Embedded  | | — ITU-T Compliance     |               |
|  | — Protobuf Schema | |   Key-Value | | — Audit Trail          |               |
|  | — Status Enums    | |   Storage   | | — Compliance Reports   |               |
|  +-------------------+ +-------------+ +------------------------+               |
|                                                                                   |
|  +=======================================================================+       |
|  |                    contracts/chaincode.go                               |       |
|  |         Optional: Hyperledger Fabric Smart Contract Anchoring           |       |
|  +=======================================================================+       |
|                                                                                   |
+===================================================================================+
|                              DATA FLOW                                            |
+===================================================================================+
|                                                                                   |
|   Operator A          Operator B          Operator C                              |
|   +------+            +------+            +------+                               |
|   | FLR  |<---gossip->| FLR  |<---gossip->| FLR  |                               |
|   | Node |  Merkle    | Node |  Merkle    | Node |                               |
|   |      |  Roots     |      |  Roots     |      |                               |
|   +--+---+            +--+---+            +--+---+                               |
|      |                    |                    |                                  |
|      +---------+----------+----------+---------+                                  |
|                |          |          |                                            |
|            +---v----------v----------v---+                                        |
|            |   AWG Junction (Passive)    |                                        |
|            |   Pre-provisioned Lambda    |                                        |
|            |   Translation Tables        |                                        |
|            +-----------------------------+                                        |
|                                                                                   |
+===================================================================================+
```

### Module Descriptions

| Module | Path | Responsibility |
|--------|------|----------------|
| **Crypto Engine** | `internal/crypto/` | ECDSA key management, lease token generation/validation, Merkle tree construction and verification, proof-of-invalidity generation |
| **Registry Engine** | `internal/registry/` | Lease CRUD lifecycle, embedded BadgerDB storage, Merkle commitment tracking, endpoint management, conflict checking |
| **Federation Manager** | `internal/federation/` | Cross-operator gossip protocol, commitment broadcasting, peer synchronization, conflict detection across operators |
| **Translation Manager** | `internal/xlat/` | Cross-domain lambda-to-lambda mapping, AWG routing table generation, vendor-neutral export format |
| **API Server** | `internal/api/` | gRPC and REST HTTP server, interceptors (auth, rate limiting, logging, recovery), streaming updates |
| **Audit & Compliance** | `internal/audit/` | ITU-T grid validation, DWDM compliance checking, hash chain integrity verification, audit report generation |
| **CLI Application** | `cmd/flr/` | Cobra-based command-line interface for all node operations |
| **Smart Contract** | `contracts/chaincode.go` | Optional Hyperledger Fabric chaincode for blockchain-anchored commitments |

---

## Quick Start

### Prerequisites

| Dependency | Version | Purpose |
|------------|---------|---------|
| Go | 1.22+ | Build toolchain |
| Make | any | Build automation |
| Protocol Buffers | 3.x+ | gRPC code generation (optional) |
| Docker | 24.x+ | Container deployment (optional) |
| Docker Compose | 2.x+ | Multi-node federation (optional) |

### 1. Clone and Build

```bash
# Clone the repository
git clone https://github.com/otap/flr.git
cd flr

# Build the binary
make build

# Verify the build
./bin/flr --help
```

### 2. Initialize Your Node

```bash
# Create required directories
mkdir -p data keys certs

# Initialize the registry with operator identity
./bin/flr init --id op-001 --name "OTAP Network Operator A"

# This generates:
#   - data/registry/       — BadgerDB database files
#   - keys/node.pem        — ECDSA P-256 private key
#   - keys/node.pub        — ECDSA P-256 public key
#   - config.yaml          — Default configuration file
```

### 3. Start the Server

```bash
# Start the gRPC and REST API servers
./bin/flr serve --config config.yaml

# Servers will listen on:
#   - gRPC:  :9090  (internal federation traffic)
#   - HTTP:  :8080  (REST API for clients)

# Check node status in another terminal
./bin/flr status --api-addr http://localhost:8080
```

### 4. Register Peer Operators

```bash
# Add a peer operator to the federation
./bin/flr operator add \
  --id op-002 \
  --name "OTAP Network Operator B" \
  --endpoint https://op-b.otap.network:9090 \
  --public-key ./keys/op-b.pub

# List registered operators
./bin/flr operator list
```

### 5. Create a Wavelength Lease

```bash
# Create a lease for a specific wavelength and endpoint
./bin/flr lease create \
  --lambda 1550.12 \
  --channel 32 \
  --band C_BAND \
  --grid 25.0 \
  --endpoint ep-001 \
  --duration 24h

# Verify the lease token
./bin/flr lease verify --lease-id <lease-id>

# List all active leases
./bin/flr lease list --status ACTIVE
```

### 6. Build and Publish a Merkle Commitment

```bash
# Build a Merkle tree from all active leases and sign the root
./bin/flr commit

# The signed commitment is automatically broadcast to all peer operators
# via the federation gossip protocol

# Verify a specific commitment
./bin/flr commit verify --operator-id op-001 --block-height 1
```

### 7. Create Cross-Operator Translations

```bash
# Create a lambda translation between operators
./bin/flr xlat create \
  --from-operator op-001 \
  --to-operator op-002 \
  --from-lambda 1550.12 \
  --to-lambda 1550.52 \
  --duration 168h

# Export routing table for passive AWG junction
./bin/flr xlat awg-export --junction junc-001
```

---

## Configuration

FLR uses a YAML configuration file that can be overridden via environment variables. The configuration is loaded using Viper, which supports hierarchical overrides (flags > env vars > config file > defaults).

### Configuration File (`config.yaml`)

```yaml
# Node identity
node:
  id: "op-001"                           # Unique operator identifier
  name: "OTAP Network Operator A"        # Human-readable operator name
  listen_addr: "0.0.0.0:9090"            # Network listen address

# Registry storage
registry:
  db_path: "./data/registry"             # BadgerDB database directory
  merkle_interval: "5m"                  # Auto-commit interval

# Cryptographic keys
crypto:
  private_key_path: "./keys/node.pem"    # ECDSA P-256 private key
  public_key_path: "./keys/node.pub"     # ECDSA P-256 public key

# Federation settings
federation:
  gossip_interval: "30s"                 # Peer sync frequency
  sync_timeout: "10s"                    # RPC timeout for peer calls
  max_peers: 50                          # Maximum peer connections

# Peer operators
operators:
  - id: "op-002"
    name: "OTAP Network Operator B"
    endpoint: "https://op-b.otap.network:9090"
    public_key_path: "./keys/op-b.pub"

# API server
server:
  grpc_addr: ":9090"                     # gRPC listen address
  http_addr: ":8080"                     # REST HTTP listen address
  tls_cert: "./certs/server.crt"         # TLS certificate
  tls_key: "./certs/server.key"          # TLS private key
  client_ca: "./certs/ca.crt"            # mTLS client CA
  enable_auth: true                      # Enable authentication

# Logging
logging:
  level: "info"                          # debug | info | warn | error
  format: "json"                         # json | text

# Compliance
compliance:
  itu_grid: "C_BAND"                     # C_BAND | L_BAND | S_BAND
  grid_spacing_ghz: 25.0                 # 25.0 | 50.0 | 100.0
```

### Environment Variable Overrides

All configuration values can be overridden using environment variables with the `FLR_` prefix:

```bash
export FLR_NODE_ID="op-001"
export FLR_NODE_NAME="My Operator"
export FLR_DB_PATH="/var/lib/flr/registry"
export FLR_GRPC_ADDR=":9090"
export FLR_HTTP_ADDR=":8080"
export FLR_LOG_LEVEL="debug"
export FLR_GOSSIP_INTERVAL="15s"
```

---

## Docker Deployment

### Single Node

```bash
# Build the Docker image
make docker

# Run a single node
mkdir -p data/keys data/certs data/registry
docker run -d \
  --name flr-node \
  -p 8080:8080 \
  -p 9090:9090 \
  -v $(pwd)/data:/data \
  -v $(pwd)/config.yaml:/config/config.yaml:ro \
  otap/flr:latest serve --config /config/config.yaml
```

### Multi-Node Federation (Docker Compose)

```bash
# Create configuration for each operator
mkdir -p config data/op-a data/op-b data/op-c

# Copy and customize config for each node
cp config.yaml.example config/op-a.yaml
cp config.yaml.example config/op-b.yaml
cp config.yaml.example config/op-c.yaml

# Edit each config with unique node.id, server ports, etc.
# Then start the full federation stack:

docker-compose up -d

# This starts:
#   - flr-node-a  (REST: http://localhost:8080, gRPC: localhost:9090)
#   - flr-node-b  (REST: http://localhost:8081, gRPC: localhost:9091)
#   - flr-node-c  (REST: http://localhost:8082, gRPC: localhost:9092)
#   - prometheus  (http://localhost:9093) — optional
#   - grafana     (http://localhost:3000) — optional

# View logs
docker-compose logs -f flr-node-a

# Stop the stack
docker-compose down
```

### With Monitoring (Prometheus + Grafana)

```bash
docker-compose --profile monitoring up -d
```

---

## CLI Reference

### Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to configuration file |
| `--api-addr` | `http://localhost:8080` | API server address |
| `--output` | `table` | Output format: `table`, `json`, `yaml` |

### Command Reference

| Command | Description |
|---------|-------------|
| `flr init` | Initialize a new registry node with operator identity and key generation |
| `flr serve` | Start the gRPC and REST API servers |
| `flr status` | Show current node status, peer connections, and registry statistics |
| `flr config` | Display current configuration (merged from file, env, and defaults) |

#### Operator Management

| Command | Description |
|---------|-------------|
| `flr operator add` | Register a peer operator in the federation |
| `flr operator list` | List all registered operators with status |
| `flr operator remove` | Remove a peer operator from the federation |

#### Endpoint Management

| Command | Description |
|---------|-------------|
| `flr endpoint add` | Register a local OTAP endpoint |
| `flr endpoint list` | List all registered endpoints |
| `flr endpoint remove` | Remove an endpoint from the registry |

#### Lease Management

| Command | Description |
|---------|-------------|
| `flr lease create` | Create a new wavelength lease with signed token |
| `flr lease get` | Retrieve lease details by ID |
| `flr lease list` | List leases with optional filters (status, operator, endpoint) |
| `flr lease renew` | Extend lease duration with new token issuance |
| `flr lease revoke` | Terminate an active lease |
| `flr lease verify` | Verify a lease token's cryptographic signature |

#### Merkle Commitments

| Command | Description |
|---------|-------------|
| `flr commit` | Build Merkle tree from active leases and sign the root |
| `flr commit verify` | Verify a commitment's signature against operator public key |

#### Cross-Operator Translation

| Command | Description |
|---------|-------------|
| `flr xlat create` | Create a cross-operator wavelength translation entry |
| `flr xlat list` | List translation entries with filters |
| `flr xlat awg-export` | Export passive AWG routing table (vendor-neutral JSON) |

#### Audit & Compliance

| Command | Description |
|---------|-------------|
| `flr audit` | Generate a comprehensive audit report |
| `flr audit compliance` | Check ITU-T grid compliance for all active leases |

---

## API Reference

### gRPC Services

The gRPC API is defined in `proto/flr/v1/registry.proto` and provides high-performance, streaming-capable endpoints for all registry operations.

#### FederatedRegistry Service

| Method | Request | Response | Description |
|--------|---------|----------|-------------|
| `CreateLease` | `CreateLeaseRequest` | `Lease` | Allocate a new wavelength lease |
| `GetLease` | `GetLeaseRequest` | `Lease` | Retrieve lease by ID |
| `RenewLease` | `RenewLeaseRequest` | `Lease` | Extend lease duration |
| `RevokeLease` | `RevokeLeaseRequest` | `Lease` | Terminate a lease |
| `ListLeases` | `ListLeasesRequest` | `ListLeasesResponse` | Query leases with filters |
| `GetMerkleCommitment` | `GetMerkleCommitmentRequest` | `MerkleCommitment` | Retrieve signed Merkle root |
| `VerifyLease` | `VerifyLeaseRequest` | `VerificationResult` | Validate lease token |
| `SubmitProofOfInvalidity` | `SubmitProofOfInvalidityRequest` | `InvalidityResult` | Report a registry violation |
| `RegisterOperator` | `RegisterOperatorRequest` | `Operator` | Add a peer operator |
| `GetOperator` | `GetOperatorRequest` | `Operator` | Retrieve operator details |
| `ListOperators` | `ListOperatorsRequest` | `ListOperatorsResponse` | List federation members |
| `CreateTranslation` | `CreateTranslationRequest` | `TranslationEntry` | Create lambda mapping |
| `GetTranslation` | `GetTranslationRequest` | `TranslationEntry` | Retrieve translation |
| `ListTranslations` | `ListTranslationsRequest` | `ListTranslationsResponse` | List translations |
| `StreamRegistryUpdates` | `StreamRegistryUpdatesRequest` | `stream RegistryUpdate` | Real-time update stream |

### REST Endpoints

All gRPC methods are exposed as REST endpoints via `grpc-gateway`:

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/v1/leases` | Create a new lease |
| `GET` | `/v1/leases/{lease_id}` | Get lease details |
| `POST` | `/v1/leases/{lease_id}/renew` | Renew a lease |
| `POST` | `/v1/leases/{lease_id}/revoke` | Revoke a lease |
| `GET` | `/v1/leases` | List leases (with query filters) |
| `GET` | `/v1/commitments` | Get latest Merkle commitment |
| `POST` | `/v1/verify` | Verify a lease token |
| `POST` | `/v1/invalidity` | Submit proof of invalidity |
| `POST` | `/v1/operators` | Register an operator |
| `GET` | `/v1/operators` | List operators |
| `POST` | `/v1/translations` | Create a translation |
| `GET` | `/v1/translations` | List translations |
| `GET` | `/v1/stream` | SSE stream of registry updates |

### Example API Calls

```bash
# Create a lease via REST
curl -X POST http://localhost:8080/v1/leases \
  -H "Content-Type: application/json" \
  -d '{
    "wavelength": {
      "lambda_nm": 1550.12,
      "channel_num": 32,
      "band": "BAND_C_BAND",
      "grid_ghz": 25.0
    },
    "endpoint_id": "ep-001",
    "operator_id": "op-001",
    "duration": {"seconds": 86400}
  }'

# Get a commitment
curl http://localhost:8080/v1/commitments?operator_id=op-001

# Verify a lease token
curl -X POST http://localhost:8080/v1/verify \
  -H "Content-Type: application/json" \
  -d '{"token": {"lease_id": "...", "signature": "..."}}'
```

---

## Project Structure

```
flr/
├── cmd/flr/                    # CLI application entry point
│   ├── main.go                 # Root command and flag parsing
│   ├── init.go                 # Node initialization command
│   ├── serve.go                # API server command
│   ├── operator.go             # Operator management commands
│   ├── endpoint.go             # Endpoint management commands
│   ├── lease.go                # Lease lifecycle commands
│   ├── commit.go               # Merkle commitment commands
│   ├── xlat.go                 # Translation table commands
│   ├── audit.go                # Audit and compliance commands
│   ├── status.go               # Node status command
│   └── config_cmd.go           # Configuration display command
│
├── internal/
│   ├── crypto/                 # Cryptographic engine
│   │   ├── engine.go           # ECDSA, SHA3-256, Merkle trees, tokens
│   │   └── engine_test.go      # Crypto unit tests
│   ├── registry/               # Core registry engine
│   │   ├── engine.go           # Lease lifecycle, Merkle building
│   │   ├── store.go            # Storage interface definition
│   │   ├── badger_store.go     # BadgerDB implementation
│   │   ├── engine_test.go      # Registry engine tests
│   │   └── badger_store_test.go# Storage tests
│   ├── federation/             # Cross-operator federation
│   │   ├── manager.go          # Federation manager
│   │   ├── client.go           # Outbound peer client
│   │   ├── gossip.go           # Gossip protocol
│   │   ├── conflict.go         # Conflict detection
│   │   ├── manager_test.go     # Federation tests
│   │   └── client_test.go      # Client tests
│   ├── xlat/                   # Lambda translation
│   │   ├── manager.go          # Translation table manager
│   │   └── manager_test.go     # Translation tests
│   ├── api/                    # API server
│   │   ├── server.go           # gRPC/REST server
│   │   ├── rest.go             # REST handler additions
│   │   ├── interceptor.go      # gRPC interceptors
│   │   └── server_test.go      # API tests
│   ├── audit/                  # Compliance and audit
│   │   ├── auditor.go          # Audit trail manager
│   │   ├── compliance.go       # ITU-T validation
│   │   ├── auditor_test.go     # Audit tests
│   │   └── compliance_test.go  # Compliance tests
│   ├── models/                 # Core data models
│   │   └── models.go           # All domain types and enums
│   └── config/                 # Configuration
│       └── config.go           # Config structs and defaults
│
├── pkg/flr/                    # Public SDK/client library
│   ├── types.go                # Re-exported public types
│   └── client.go               # Go client for FLR API
│
├── proto/flr/v1/               # Protocol Buffers schema
│   ├── registry.proto          # Service and message definitions
│   ├── registry.pb.go          # Generated Go structs
│   ├── registry_grpc.pb.go     # Generated gRPC service
│   └── registry.pb.gw.go       # Generated REST gateway
│
├── contracts/                  # Hyperledger Fabric chaincode
│   ├── chaincode.go            # Smart contract implementation
│   └── chaincode_test.go       # Chaincode unit tests
│
├── test/                       # Test suites
│   ├── integration/            # Integration tests
│   │   ├── lease_lifecycle_test.go
│   │   ├── merkle_test.go
│   │   └── federation_sync_test.go
│   ├── fixtures/               # Test data
│   │   ├── operators.json
│   │   ├── leases.json
│   │   └── keys/
│   │       └── generate.go
│   └── helper.go               # Test utilities
│
├── bin/                        # Build output directory
├── data/                       # Runtime data (BadgerDB)
├── keys/                       # Cryptographic key storage
├── certs/                      # TLS certificates
├── config/                     # Per-operator configs (Docker)
├── monitoring/                 # Prometheus/Grafana configs
├── config.yaml.example         # Example configuration
├── docker-compose.yml          # Multi-node federation stack
├── Dockerfile                  # Multi-stage container build
├── Makefile                    # Build automation
├── go.mod                      # Go module definition
└── go.sum                      # Go module checksums
```

---

## Cryptographic Design

FLR employs a multi-layered cryptographic architecture designed to provide integrity, authenticity, and non-repudiation across all registry operations.

### Digital Signatures: ECDSA P-256

All operator identities are based on **ECDSA with the NIST P-256 curve** (also known as secp256r1). Each operator generates a unique P-256 key pair on initialization. Private keys never leave the operator node; public keys are exchanged during operator registration and pinned for the lifetime of the federation relationship. All Merkle commitments, lease tokens, and audit log entries are signed with the operator's private key and verified by peers using the pinned public key.

### Hash Function: SHA3-256

FLR uses **SHA3-256 (Keccak)** as its primary hash function for:

- **Lease hashing**: Each lease record is serialized to canonical JSON and hashed with SHA3-256 to produce a leaf node hash
- **Merkle tree construction**: Parent nodes are computed as `SHA3-256(left || right)` in standard binary Merkle tree fashion
- **Token hashing**: Lease tokens are hashed for inclusion in the token hash chain
- **Audit log chaining**: Each audit entry includes the SHA3-256 hash of the previous entry, creating an immutable hash chain

### Merkle Tree Commitments

The Merkle tree is built over all active leases, sorted deterministically by lease ID to ensure reproducible tree structures:

```
                     [Root Hash]  <-- Signed + Timestamped = Commitment
                    /           \
              [H(abcd)]      [H(efgh)]
              /       \        /       \
         [H(ab)]   [H(cd)] [H(ef)]   [H(gh)]
          /   \     /   \    /   \     /   \
       Ha     Hb  Hc    Hd He    Hf  Hg    Hh
        |      |   |     |  |     |   |     |
      L_a    L_b L_c  L_d L_e  L_f L_g  L_h
```

The signed Merkle root, combined with timestamp, operator ID, and lease count, forms a **MerkleCommitment** that is broadcast to all peers. Any tampering with a lease record after commitment would change its leaf hash and invalidate the root — a fact verifiable by anyone holding the operator's public key.

### Lease Tokens

Each lease is accompanied by a **LeaseToken** containing:

| Field | Type | Purpose |
|-------|------|---------|
| `Version` | `int32` | Token format version (current: 1) |
| `LeaseID` | `string` | Unique lease identifier (UUID) |
| `OperatorID` | `string` | Issuing operator identity |
| `Wavelength` | `Wavelength` | Full wavelength specification |
| `EndpointID` | `string` | Allocated endpoint reference |
| `StartTime` | `time.Time` | Lease validity start |
| `EndTime` | `time.Time` | Lease validity end |
| `Nonce` | `[]byte` | 32-byte random anti-replay value |
| `Signature` | `[]byte` | ECDSA signature over all above fields |
| `IssuedAt` | `time.Time` | Token issuance timestamp |

Token verification involves: (1) checking the ECDSA signature against the operator's pinned public key, (2) verifying the nonce has not been replayed, (3) confirming the current time falls within the validity window, and (4) cross-referencing the token hash against the stored Merkle commitment.

---

## Federation Protocol

FLR implements a gossip-based federation protocol for maintaining eventual consistency across independent operator registries.

### Peer Registration

When an operator wants to join a federation, it registers each peer's identity (ID, name, public key, API endpoint) in its local configuration. On startup, the federation manager validates all peer public keys and establishes persistent connections.

### Commitment Broadcasting

After a local operator builds and signs a Merkle commitment, the commitment is broadcast to all registered peers:

1. **Local Commit**: The registry engine builds a Merkle tree over all active leases, computes the root hash, and signs it with the operator's private key
2. **Gossip Broadcast**: The `FederationManager.PushCommitment()` method sends the signed commitment to every registered peer's API endpoint
3. **Remote Validation**: Each receiving peer validates the signature against the sender's pinned public key and stores the commitment locally
4. **Conflict Detection**: If a peer's local state indicates a conflict with the received commitment, a `ProofOfInvalidity` is generated and submitted back to the originator

### Synchronization

Periodic gossip rounds ensure all operators converge on a consistent view:

```
+--------+                    +--------+
| Node A |                    | Node B |
+---+----+                    +---+----+
    |                             |
    |  1. GetLatestCommitment()   |
    |---------------------------->|
    |  <commitment, height=N>     |
    |<----------------------------|
    |                             |
    |  2. If behind: StreamUpdates|
    |---------------------------->|
    |  <RegistryUpdate stream>    |
    |<----------------------------|
    |                             |
    |  3. DetectConflicts()       |
    |  (local scan)               |
    |                             |
    |  4. If conflict: SubmitPoI  |
    |---------------------------->|
    |  <InvalidityResult>         |
    |<----------------------------|
```

### Conflict Resolution

The `ProofOfInvalidity` mechanism enables operators to cryptographically prove violations:

- **Double-Allocation**: Two leases allocated for the same wavelength with overlapping time windows
- **Expired Lease**: A lease that remains marked as active past its `EndTime`
- **Invalid Signature**: A lease token with an unverifiable signature
- **Unauthorized Operation**: A mutation from an unrecognized or suspended operator

Each PoI includes a Merkle proof path from the offending lease leaf to the committed root, enabling third-party verification without revealing the full registry contents.

---

## Compliance

### ITU-T Grid Validation

FLR enforces strict compliance with **ITU-T G.694.1** DWDM grid standards. All wavelength assignments are validated against the configured band and grid spacing:

- **C-Band**: 1530.33 nm to 1565.50 nm (conventional erbium-doped fiber amplifier range)
- **L-Band**: 1565.50 nm to 1625.50 nm (long wavelength extension)
- **S-Band**: 1460.00 nm to 1530.00 nm (short wavelength)

Grid spacing options: **25 GHz**, **50 GHz**, or **100 GHz** (corresponding to approximately 0.2 nm, 0.4 nm, or 0.8 nm channel spacing in the C-band).

The `internal/audit` module provides `ValidateDWDMGrid(lambdaNm, gridGHz)` which returns a boolean indicating whether the wavelength falls on the standard grid, and the corresponding ITU-T channel number.

### Audit Trails

Every registry mutation is recorded as an `AuditLogEntry` with the following properties:

- **Hash-chained**: Each entry includes the SHA3-256 hash of the previous entry, creating a tamper-evident chain
- **Operation-typed**: All operations are categorized (`CREATE_LEASE`, `REVOKE_LEASE`, `COMMIT_MERKLE`, `REGISTER_OPERATOR`, etc.)
- **Timestamped**: All entries carry UTC timestamps with nanosecond precision
- **Attributable**: Every entry records the operator ID responsible for the mutation
- **Structured details**: Operation-specific details are JSON-encoded for machine parsing

The `flr audit` command generates a comprehensive compliance report including total lease counts, active/expired/revoked breakdowns, operator status summary, commitment history, hash chain integrity verification, and a violations list with severity classification.

---

## Development

### Build Targets

Run `make help` to see all available targets:

```bash
$ make help
FLR — Federated Lambda Registry

Available targets:
  build           Build the flr binary
  test            Run all tests with coverage
  unit-test       Run unit tests only
  integration-test  Run integration tests only
  proto           Generate protobuf Go code
  fmt             Format Go code
  vet             Run go vet
  lint            Run linter
  docker          Build Docker image
  deploy-local    Start local stack (docker-compose)
  stop-local      Stop local stack
  logs            View node logs
  clean           Clean build artifacts
  coverage        Generate HTML coverage report
  dev-init        Initialize dev environment
  help            Show this help
```

### Testing

```bash
# Run all tests with race detection and coverage
make test

# Run only unit tests
make unit-test

# Run only integration tests
make integration-test

# Generate HTML coverage report
make coverage
# Open coverage.html in browser
```

### Protocol Buffer Code Generation

After modifying `proto/flr/v1/registry.proto`, regenerate the Go code:

```bash
make proto
```

This requires the Protocol Buffers compiler (`protoc`) with the Go, gRPC, and grpc-gateway plugins.

### Code Quality

```bash
# Format all Go source files
make fmt

# Run static analysis
make vet

# Run linter (requires golangci-lint)
make lint
```

### Contributing

Contributions are welcome. Please follow these guidelines:

1. **Fork and branch**: Create a feature branch from `main`
2. **Code style**: Follow standard Go conventions (`go fmt`, `go vet`)
3. **Tests**: Include unit tests for all new functionality; integration tests for cross-module features
4. **Commits**: Use clear, descriptive commit messages
5. **Documentation**: Update this README for any user-facing changes
6. **Pull requests**: Open PRs against `main` with a clear description of changes

---

## License

This project is licensed under the **MIT License**. See the [LICENSE](LICENSE) file for details.

```
MIT License

Copyright (c) 2025 OTAP Network Contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## Patent Notice

This software implements concepts described in **Patent Claim #9: "Cryptographically Federated Wavelength Registry with Tamper-Evident Lease Tokenization."** The patent covers the novel combination of Merkle-tree-based registry commitments, cryptographically signed lease tokenization, and cross-operator federation with proof-of-invalidity conflict detection for multi-operator optical network resource management.

The software is provided under the MIT license for research, development, and commercial use in accordance with the terms specified in the patent filing. Organizations deploying this software in production environments should consult their legal counsel regarding patent licensing requirements.

---

## Acknowledgments

FLR was built for the OTAP (Optical Transport Access Platform) ecosystem. Special thanks to the contributors and the optical networking research community for their work on passive optical systems, wavelength routing, and federated resource management.

For questions, bug reports, or feature requests, please open an issue on the project repository.
