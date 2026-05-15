# DESIGN.md — Federated Lambda Registry (FLR) v1.0

## Technical Design Document

**Version:** 1.0  
**Date:** 2024  
**Classification:** Internal Technical Documentation  
**Audience:** System Architects, Security Engineers, DevOps, Telecommunications Protocol Engineers

---

## Table of Contents

1. [Design Philosophy](#1-design-philosophy)
2. [System Architecture](#2-system-architecture)
3. [Data Model Design](#3-data-model-design)
4. [Cryptographic Design](#4-cryptographic-design)
5. [Registry Engine Design](#5-registry-engine-design)
6. [Storage Design](#6-storage-design)
7. [Federation Protocol Design](#7-federation-protocol-design)
8. [API Design](#8-api-design)
9. [Audit & Compliance Design](#9-audit--compliance-design)
10. [Chaincode Design](#10-chaincode-design)
11. [Security Model](#11-security-model)
12. [Performance Considerations](#12-performance-considerations)
13. [Failure Handling](#13-failure-handling)
14. [Deployment Architecture](#14-deployment-architecture)

---

## 1. Design Philosophy

### 1.1 Purpose and Vision

The Federated Lambda Registry (FLR) is a cryptographically secured, distributed system for managing wavelength (lambda) assignments across multiple Optical Transport Access Point (OTAP) network operators. It implements the "Cryptographically Federated Wavelength Registry with Tamper-Evident Lease Tokenization" concept, providing a trustless federation layer where operators can independently manage their wavelength allocations while cryptographically proving the integrity of their registries to peers.

### 1.2 Key Design Principles

| Principle | Rationale | Implementation |
|-----------|-----------|----------------|
| **Zero-Trust Federation** | No operator trusts another implicitly | Every registry claim is cryptographically signed and independently verifiable via Merkle proofs |
| **Passive-Compatible AWG** | Arrayed Waveguide Grating junctions have zero electrical processing capability | Translation tables are pre-provisioned and cryptographically signed before deployment |
| **Deterministic Cryptography** | All peers must reach the same conclusions | Sorted Merkle trees, canonical JSON serialization, lexicographic ordering |
| **Minimal External Dependencies** | Telecom equipment runs in constrained environments | Embedded BadgerDB instead of external database; single Go binary deployment |
| **Tamper-Evident Audit Trail** | Regulatory compliance requires immutable history | Hash-chained audit logs with SHA3-256; every mutation is logged with prev-hash linking |
| **Standards Compliance** | Must interoperate with ITU-T DWDM equipment | Full ITU-T G.694.1 grid validation; C/L/S-band compliance checking |

### 1.3 Why This Architecture

The FLR adopts a **federated mesh** topology rather than a centralized or fully-replicated design for three critical reasons:

1. **Sovereignty**: Each operator maintains full control over their own wavelength allocations. No central authority can revoke or override assignments.

2. **Scalability**: Each node only stores its own leases plus commitments from peers. Storage grows as O(n) with local leases, not O(N) with federation size.

3. **Availability**: The system degrades gracefully when peers are unreachable. Local registry operations (lease creation, renewal, revocation) continue uninterrupted.

---

## 2. System Architecture

### 2.1 High-Level Component Diagram

```
+=========================================================================================+
|                           EXTERNAL INTERFACES                                           |
|  +-------------------+  +-------------------+  +-------------------+                    |
|  |   gRPC Clients    |  |   REST Clients    |  |   Peer Operators  |                    |
|  |   (Port 9090)     |  |   (Port 8080)     |  |   (mTLS/HTTPS)    |                    |
|  +--------+----------+  +--------+----------+  +--------+----------+                    |
+===========|=====================|=====================|=================================+
            |                     |                     |
+-----------v---------------------v---------------------v---------------------------------+
|                              API SERVER (internal/api)                                  |
|  +-----------------+  +-----------------+  +------------------+  +------------------+   |
|  | gRPC Server     |  | HTTP Gateway    |  | Stream Handler   |  | Auth/Rate Limit  |   |
|  | (grpc-gateway)  |  | (REST mapping)  |  | (SSE updates)    |  | (interceptors)   |   |
|  +--------+--------+  +--------+--------+  +--------+---------+  +--------+---------+   |
+----------|---------------------|---------------------|------------------|---------------+
           |                     |                     |                   |
+----------v---------------------v---------------------v-------------------v---------------+
|                            INTERNAL MODULES                                             |
|                                                                                         |
|  +------------------+     +------------------+     +------------------+                 |
|  | Registry Engine  |     | Federation Mgr   |     | Translation Mgr  |                 |
|  | (internal/reg)   |<--->| (internal/fed)   |<--->| (internal/xlat)  |                 |
|  | Lease lifecycle  |     | Gossip, sync,    |     | AWG table gen,   |                 |
|  | State machine    |     | Conflict detect  |     | Cross-domain map |                 |
|  +--------+---------+     +--------+---------+     +--------+---------+                 |
|           |                        |                        |                           |
|           v                        v                        v                           |
|  +------------------+     +------------------+     +------------------+                 |
|  | Crypto Engine    |     | Federation Client|     | Audit System     |                 |
|  | (internal/cr)    |     | (internal/fed)   |     | (internal/audit) |                 |
|  | ECDSA, SHA3-256  |     | HTTP outbound    |     | ITU-T, hash chain|                 |
|  | Merkle trees     |     | Peer comms       |     | Compliance       |                 |
|  +--------+---------+     +--------+---------+     +--------+---------+                 |
|           |                        |                        |                           |
+-----------|------------------------|------------------------|---------------------------+
            |                        |                        |
+-----------v------------------------v------------------------v---------------------------+
|                              STORAGE LAYER                                              |
|                                                                                         |
|  +------------------+     +------------------+     +------------------+                 |
|  | BadgerDB (KV)    |     | Store Interface  |     | Key-Prefixed     |                 |
|  | Embedded LSM     |<----| (Abstraction)    |<----| Layout           |                 |
|  | Zero external    |     | Pluggable impl   |     | lease:, ep:, op: |                 |
|  | dependencies     |     |                  |     | commitment:,     |                 |
|  |                  |     |                  |     | audit:           |                 |
|  +------------------+     +------------------+     +------------------+                 |
|                                                                                         |
+=========================================================================================+
                                    |
                                    v
+-----------------------------------------------------------------------------------------+
|                              HYPERLEDGER FABRIC                                         |
|                                                                                         |
|  +------------------+     +------------------+     +------------------+                 |
|  | Chaincode        |     | Commitment Store |     | Proof of         |                 |
|  | (Smart Contract) |     | (On-chain)       |     | Invalidity Store |                 |
|  | Go contract API  |     | Operator history |     | (Immutable)      |                 |
|  +------------------+     +------------------+     +------------------+                 |
|                                                                                         |
+-----------------------------------------------------------------------------------------+
```

### 2.2 Module Dependency Graph

```
                    +-----------------+
                    |   cmd/flr       |
                    |   (CLI entry)   |
                    +--------+--------+
                             |
        +--------------------+--------------------+
        |                    |                    |
        v                    v                    v
+-------+-------+   +--------+--------+  +------+------+
|   api/server  |   | registry/engine |  | federation  |
|   (gRPC+REST) |   |   (lease mgmt)  |  |   manager   |
+-------+-------+   +--------+--------+  +------+------+
        |                    |                    |
        v                    v                    v
+-------+-------+   +--------+--------+  +------+------+
|   crypto      |   |  store (iface)  |  |   client    |
|   engine      |   |  badger_store   |  |   (HTTP)    |
+-------+-------+   +--------+--------+  +------+------+
        |                    |                    |
        +--------------------+--------------------+
                             |
                             v
                    +--------+--------+
                    | internal/models |
                    |  (shared types) |
                    +-----------------+
                             |
                             v
                    +--------+--------+
                    |  contracts/     |
                    |  chaincode.go   |
                    | (Hyperledger)   |
                    +-----------------+
```

### 2.3 Data Flow — Lease Lifecycle

```
[Client]          [API Server]       [Registry Engine]     [Crypto Engine]     [BadgerDB]
   |                    |                    |                    |                |
   |--- CreateLease --->|                    |                    |                |
   |                    |--- AllocateLease ->|                    |                |
   |                    |                    |--- CheckConflict ->|                |
   |                    |                    |<-- no conflict ----|                |
   |                    |                    |--- GenLeaseToken ->|                |
   |                    |                    |<-- token ----------|                |
   |                    |                    |--- CreateLease --------------------->|
   |                    |<-- Lease + Token --|                    |                |
   |<-- Lease response --|                    |                    |                |
   |                    |                    |                    |                |
   |--- VerifyLease --->|                    |                    |                |
   |                    |--- VerifyLease --->|                    |                |
   |                    |                    |--- GetLease ------------------------>|
   |                    |                    |<-- lease record ---|                |
   |                    |                    |--- ValidateToken ->|                |
   |                    |                    |<-- valid ----------|                |
   |<-- true/false ------|                    |                    |                |
```

### 2.4 Data Flow — Federation Gossip

```
+-----------+     gossip.Round()     +-----------+
| Operator  |<---------------------->| Operator  |
| Node A    |   1. SyncWithOperator  | Node B    |
|           |   2. DetectConflicts   |           |
|           |   3. PushCommitment    |           |
+-----------+                        +-----------+
     |                                      |
     |  1a. GET /v1/commitments/op-B/0      |
     |------------------------------------->|
     |<-------------------------------------|
     |  1b. GET /v1/leases?status=active    |
     |------------------------------------->|
     |<-------------------------------------|
     |                                      |
     |  3. POST /v1/commitments             |
     |------------------------------------->|
     |<-------------------------------------|
```

---

## 3. Data Model Design

### 3.1 Entity Relationship Diagram

```
+-------------+        +-------------+        +--------------+
|  Operator   |1------*|  Endpoint   |1------*|    Lease     |
|             |        |             |        |              |
| id (PK)     |        | id (PK)     |        | id (PK)      |
| name        |        | node_id     |        | wavelength   |<>---> Wavelength
| public_key  |        | operator_id |FK      | endpoint_id  |FK
| endpoint    |        | address     |        | operator_id  |FK
| status      |        | awg_port    |        | status       |
| joined_at   |        | coordinates |        | start_time   |
| last_seen   |        | status      |        | end_time     |
+-------------+        +-------------+        | token_hash   |
       | 1                                    | parent_hash  |
       |                                        +------+-------+
       |                                               |
       | *                                             | 1
+------v-------+                               +-------v--------+
| Translation  |                               | MerkleCommit   |
| Entry        |                               |                |
|              |                               | operator_id    |
| id (PK)      |                               | root_hash      |
| from_op      |                               | timestamp      |
| to_op        |                               | signature      |
| from_wl      |<>---> Wavelength              | lease_count    |
| to_wl        |<>---> Wavelength              | block_height   |
| from_port    |                               +----------------+
| to_port      |
| status       |        +-------------+
| eff_time     |        | AuditLog    |
| exp_time     |        |             |
+--------------+        | timestamp   |
                        | operation   |
                        | operator_id |
                        | lease_id    |
                        | details     |
                        | hash        |<-- SHA3-256
                        | prev_hash   |<-- chain link
                        +-------------+

+-------------+        +------------------+
| LeaseToken  |        | ProofOfInvalidity|
|             |        |                  |
| version     |        | type             |
| lease_id    |<>------| lease_a          |
| operator_id |        | lease_b          |
| wavelength  |<>----->| commitment       |
| endpoint_id |        | merkle_proof[]   |
| start_time  |        | timestamp        |
| end_time    |        +------------------+
| nonce       |
| signature   |
| issued_at   |
+-------------+
```

### 3.2 Key Field Explanations

**Wavelength.ToKey()** — Generates a deterministic unique key string in the format `lambdaNm:channelNum:band:gridGHz` (e.g., `"1550.12:21:C_BAND:25.0"`). This key is the canonical identifier for conflict detection. Two wavelengths with the same key are considered identical for allocation purposes.

**Lease.TokenHash** — The SHA3-256 hash of the canonicalized LeaseToken fields. Stored on the lease record to enable efficient token validation without regenerating the token. Any tampering with the token produces a hash mismatch.

**Lease.ParentHash** — Links each lease to the chronologically previous lease in a hash chain. This creates an immutable append-only history of all allocations, enabling auditors to verify that no lease has been retroactively inserted or removed.

**MerkleCommitment.BlockHeight** — A monotonically increasing counter local to each operator. It serves as a logical clock for federation synchronization; peers request updates "since block height N."

### 3.3 Design Decisions

| Decision | Rationale |
|----------|-----------|
| UUID v4 for Lease IDs | Global uniqueness without coordination; `google/uuid` library |
| Canonical JSON for hashing | Human-readable, deterministic, language-independent serialization |
| RFC3339Nano for timestamps | Nanosecond precision, lexicographically sortable, unambiguous timezone |
| Separate `EndpointID` FK (not embedded) | Endpoints are managed independently; leases reference them by ID to avoid denormalization |
| Status as int32 enums | Protobuf-compatible, compact storage, fast comparison |

---

## 4. Cryptographic Design

### 4.1 ECDSA P-256 Key Management

```
+-------------------------------------------------------------------+
|                    KEY PAIR LIFECYCLE                              |
+-------------------------------------------------------------------+
|                                                                    |
|  Generation                  Storage                  Usage        |
|  +--------+               +--------+              +---------+      |
|  | crypto |               | PEM    |              | crypto  |      |
|  | Generate|               | files  |              | Engine  |      |
|  | KeyPair |               |        |              |         |      |
|  +---+----+               +---+----+              +----+----+      |
|      |                        |                        |           |
|      v                        v                        v           |
|  ECDSA P-256              PKCS#8/SEC1              Sign tokens     |
|  elliptic.P256()          PEM encoding             Sign commitments |
|  crypto/rand              ~/.flr/keys/             Verify peers    |
|                                                                    |
|  Private Key: 32 bytes scalar                                      |
|  Public Key:  64 bytes (X,Y uncompressed)                          |
|  Signature:   64 bytes (R,S) ASN.1/DER or raw                      |
+-------------------------------------------------------------------+
```

The FLR uses **ECDSA over the NIST P-256 curve** (secp256r1) for all signing operations. This choice balances security (128-bit equivalent symmetric security) with performance and wide hardware acceleration support.

**Key Generation**: `ecdsa.GenerateKey(elliptic.P256(), rand.Reader)` produces a fresh keypair. Private keys are encoded as PKCS#8 or SEC1 EC PRIVATE KEY PEM blocks. Public keys use PKIX ASN.1 DER encoding wrapped in PEM.

**Key Validation**: The `NewEngine()` constructor enforces that only P-256 keys are accepted. Attempting to load a P-384 or P-521 key returns an explicit error: `"only P-256 curve is supported"`.

**Signature Format**: Signatures are encoded as 64-byte fixed-length arrays — 32 bytes for R and 32 bytes for S, zero-padded. This avoids ASN.1 parsing variability and ensures deterministic signature lengths in tokens and commitments.

### 4.2 SHA3-256 Hashing Strategy

The system uses **SHA3-256** (Keccak variant standardized by NIST) as its primary hash function. SHA3-256 was chosen over SHA-256 for:

1. **Different security assumptions**: SHA3 is based on sponge construction, providing security even if Merkle-Damgard vulnerabilities are discovered.
2. **Domain separation**: Distinct from Bitcoin/Ethereum SHA-256 chains, preventing cross-protocol attacks.
3. **Future-proofing**: 256-bit output provides collision resistance of 2^128 operations.

**Hashing Pipeline**:
```
Lease Record -> Canonical JSON -> SHA3-256 -> 32-byte Hash
                                              |
                        +--------------------+--------------------+
                        |                    |                    |
                        v                    v                    v
                  Merkle Tree         Token Comparison    Audit Chain
                  (leaf hash)         (token hash)        (entry hash)
```

### 4.3 Merkle Tree Construction

The FLR builds a **binary, sorted, power-of-2-padded** Merkle tree over all active leases.

```
                    +------------------+
                    |    Root Hash     |  <-- Signed commitment target
                    |   SHA3-256(AB)   |
                    +--------+---------+
                             |
              +--------------+--------------+
              |                             |
       +------v------+              +------v------+
       |  Hash(AB)   |              |  Hash(C0)   |
       | SHA3(A|B)   |              | SHA3(C|0)   |
       +------+------+              +------+------+
              |                             |
        +-----+-----+                 +-----+-----+
        |           |                 |           |
   +----v----+ +----v----+      +----v----+ +----v----+
   | Hash(A) | | Hash(B) |      | Hash(C) | | ZeroHash|  <- padding
   | Lease_1 | | Lease_2 |      | Lease_3 | | (dummy) |
   +---------+ +---------+      +---------+ +---------+
```

**Construction Algorithm**:

1. **Sort**: All active leases are sorted by `LeaseID` (lexicographic) to ensure deterministic ordering across all operators.
2. **Hash leaves**: Each lease is canonicalized to JSON (`canonicalLeaseFields`), hashed with SHA3-256 to produce a 32-byte leaf hash.
3. **Pad to power of 2**: If there are N leases, pad with zero-hash leaves to reach the next power of 2 (e.g., 3 leases → 4 leaves; 5 leases → 8 leaves).
4. **Build bottom-up**: Pairs of sibling hashes are concatenated and hashed: `parent = SHA3-256( left || right )`.
5. **Recursive**: The process repeats until a single root hash remains.

**Why sorted?** Deterministic tree construction means any operator can independently rebuild the tree and verify the root hash matches the signed commitment.

**Why power-of-2 padded?** Guarantees a complete binary tree, simplifying proof generation and ensuring every level is fully populated.

### 4.4 Lease Token Structure and Signing

```
+------------------------------------------------------------------+
|                     LEASE TOKEN STRUCTURE                         |
+------------------------------------------------------------------+
|  Field        | Type        | Description                         |
|---------------|-------------|-------------------------------------|
|  Version      | int32       | Token format version (currently 1)  |
|  LeaseID      | string      | Unique lease identifier (UUID)      |
|  OperatorID   | string      | Issuing operator identity           |
|  Wavelength   | Wavelength  | Full ITU-T wavelength specification |
|  EndpointID   | string      | Target endpoint for the lambda      |
|  StartTime    | RFC3339Nano | Lease validity start (UTC)          |
|  EndTime      | RFC3339Nano | Lease validity end (UTC)            |
|  Nonce        | [32]byte    | Cryptographic random nonce          |
|  Signature    | [64]byte    | ECDSA P-256 signature (R||S)       |
|  IssuedAt     | time.Time   | Token issuance timestamp            |
+------------------------------------------------------------------+
```

**Signing Process**:
```
1. Create canonicalTokenFields struct (excludes Signature, IssuedAt)
2. Marshal to canonical JSON with RFC3339Nano timestamps (UTC)
3. Compute SHA3-256 hash of JSON bytes
4. Sign hash with ECDSA P-256 private key → (R, S) big integers
5. Marshal R and S to 32-byte fixed-width arrays, concatenate → 64-byte signature
6. Attach signature and issued-at timestamp to token
```

**Nonce Purpose**: The 32-byte random nonce prevents token replay attacks and ensures that even identical lease parameters produce distinct token hashes. Generated via `crypto/rand.Read()`.

### 4.5 Merkle Proof Generation and Verification

A Merkle proof consists of the **sibling path** from a leaf hash to the root — the sequence of sibling hashes needed to recompute the root.

```
Proof for Lease B:

                    [Root]
                     /  \
                   [AB] [C0]
                   /\      /\
                 [A][B]  [C][0]

MerkleProof = [ Hash(A), Hash(C0) ]
              ^ sibling   ^ sibling
                at L0      at L1

Verification:
  hash = SHA3-256( Hash(A) || Hash(B) )   → Hash(AB)
  hash = SHA3-256( Hash(AB) || Hash(C0) ) → Root
  Compare computed Root against commitment.RootHash
```

**Lexicographic Ordering in Proofs**: During verification, sibling hashes are concatenated in lexicographic order (smaller first) before hashing. This ensures deterministic verification regardless of which side of the tree the leaf falls on.

### 4.6 Proof of Invalidity Construction

A ProofOfInvalidity (PoI) cryptographically demonstrates that a registry violation exists:

```
+------------------------------------------------------------------+
|              PROOF OF INVALIDITY STRUCTURE                        |
+------------------------------------------------------------------+
|                                                                    |
|  Type: DOUBLE_ALLOCATION                                           |
|                                                                    |
|  LeaseA:  {Op-A, lambda=1550.12nm, start=T1, end=T2}              |
|  LeaseB:  {Op-B, lambda=1550.12nm, start=T3, end=T4}              |
|           ^ same wavelength, overlapping time windows             |
|                                                                    |
|  Commitment:  Signed Merkle root from offending operator           |
|  MerkleProof: [sibling hashes] proving LeaseA is in the tree      |
|  Timestamp:   When the proof was generated                         |
|                                                                    |
+------------------------------------------------------------------+
```

**Verification Steps**:
1. Verify the commitment signature using the operator's public key.
2. Compute `leafHash = SHA3-256(canonicalJSON(LeaseA))`.
3. Follow the Merkle proof path, hashing with siblings at each level.
4. Compare the computed root against `commitment.RootHash`.
5. Independently verify that LeaseA and LeaseB represent a real conflict (same wavelength, overlapping times).

**PoI Types**:

| Type | Trigger | Required Fields |
|------|---------|----------------|
| `DOUBLE_ALLOCATION` | Same lambda, overlapping time, different operators | LeaseA, LeaseB, MerkleProof |
| `EXPIRED_LEASE` | Lease end_time < now but status != EXPIRED | LeaseA, MerkleProof |
| `INVALID_SIGNATURE` | Token signature fails verification | Commitment |
| `UNAUTHORIZED_OP` | Operator not in federation registry | LeaseA |

---

## 5. Registry Engine Design

### 5.1 Lease State Machine

```
                         +-----------+
                    +--->|  ACTIVE   |<------------------+
                    |    +-----+-----+                     |
                    |          |                           |
         Allocate   |    Renew |                           | Revoke
            |       |    Lease |                           |
            v       |          v                           v
     +----------+  |    +-----------+               +-----------+
     |  PENDING   |--+   |  ACTIVE   |               |  REVOKED  |
     +----------+      |  (extended) |               +-----------+
                       +-----------+
                             |
                             | EndTime < Now
                             v
                       +-----------+
                       |  EXPIRED  |
                       +-----------+
```

**State Transitions**:

| From | To | Trigger | Authorization |
|------|-----|---------|---------------|
| PENDING | ACTIVE | `AllocateLease()` | Local operator only |
| ACTIVE | ACTIVE (renewed) | `RenewLease(extension)` | Same operator, before expiry |
| ACTIVE | REVOKED | `RevokeLease()` | Issuing operator |
| ACTIVE | EXPIRED | `ExpireLeases()` (background) | System, when `Now > EndTime` |
| EXPIRED | — | — | Terminal state |
| REVOKED | — | — | Terminal state |

### 5.2 Conflict Detection Algorithm

The registry engine prevents double-allocation through a pessimistic conflict check at allocation time:

```
CheckConflict(wavelength, excludeLeaseID):
  1. Compute wlKey = wavelength.ToKey()
  2. List all ACTIVE leases for this operator
  3. For each lease:
     a. Skip if lease.ID == excludeLeaseID (for renewals)
     b. Skip if lease.Wavelength.ToKey() != wlKey
     c. Skip if Now >= lease.EndTime (already expired)
     d. If all checks pass → CONFLICT FOUND
  4. Return (conflictingLease, true) or (nil, false)
```

The check acquires a **read lock** (`RLock`) to allow concurrent allocations on different wavelengths while ensuring a consistent view during each check.

### 5.3 Lease Lifecycle Methods

| Method | Lock Type | Steps |
|--------|-----------|-------|
| `AllocateLease` | Write Lock | Conflict check → Create lease → Generate token → Hash token → Store → Audit log |
| `RenewLease` | Write Lock | Get lease → Validate active → Extend end time → Generate new token → Update store → Audit log |
| `RevokeLease` | Write Lock | Get lease → Validate not already revoked → Set REVOKED → Update store → Audit log |
| `ExpireLeases`| Write Lock | List active → Find expired → Set EXPIRED → Batch update → Audit log each |
| `BuildMerkleTree` | Read Lock | List active → Delegate to crypto engine |
| `CommitMerkleTree` | Write Lock | Build tree → Increment blockHeight → Sign commitment → Store commitment → Audit log |

### 5.4 Block Height Semantics

Each operator maintains an independent `blockHeight` counter in the registry engine. This is **not** a blockchain block height but a logical monotonic counter for commitment versioning:

- Incremented by 1 each time `CommitMerkleTree()` is called.
- Used in commitment keys: `commitment:<operatorID>:<height>`.
- Enables peers to request "all commitments since height N."
- Enables audit of historical registry states.

---

## 6. Storage Design

### 6.1 Why Embedded KV (BadgerDB)

| Criterion | BadgerDB | PostgreSQL | etcd |
|-----------|----------|------------|------|
| External dependencies | Zero | Server process | Server cluster |
| Deployment complexity | Single binary | Docker/service | 3-node minimum |
| Write throughput | 100K+ ops/s | 10K ops/s | 10K ops/s |
| Embedded | Yes | No | No |
| LSM-tree | Yes (optimized writes) | B-tree | B-tree |

BadgerDB v4 uses an **LSM-tree (Log-Structured Merge-tree)** with value log separation, providing excellent write throughput for the append-heavy workload of lease creation and audit logging.

### 6.2 Key Layout

```
BadgerDB Key Space
+--------------------------------------------------------------+
|  Prefix       | Key Format              | Example             |
|---------------|-------------------------|---------------------|
|  lease:       | lease:<lease_id>        | lease:a1b2-c3d4     |
|  endpoint:    | endpoint:<endpoint_id>  | endpoint:node-01    |
|  operator:    | operator:<operator_id>  | operator:op-001     |
|  commitment:  | commitment:<op>:<h>    | commitment:op-001:5 |
|  commitment:  | commitment:<op>:latest | commitment:op-001:l |
|  audit:       | audit:<RFC3339Nano>    | audit:2024-01-01... |
+--------------------------------------------------------------+
```

**Key Design Rationale**:
- **Prefixed keys**: Enable efficient prefix scans via `ValidForPrefix()` for listing operations.
- **Commitment dual-key**: Stored both by height (for historical queries) and as a "latest" pointer (for fast lookups).
- **Audit timestamp keys**: RFC3339Nano format ensures chronological ordering when iterating.

### 6.3 Indexing Strategy

BadgerDB is a pure KV store with no secondary indexes. The FLR implements **filtered iteration**:

```
ListLeases(filter):
  1. Create iterator with prefix "lease:"
  2. For each key-value pair:
     a. Unmarshal JSON → Lease struct
     b. If filter.OperatorID != "" AND lease.OperatorID != filter.OperatorID → skip
     c. If filter.EndpointID != "" AND lease.EndpointID != filter.EndpointID → skip
     d. If filter.Status != 0 AND lease.Status != filter.Status → skip
     e. Otherwise, append to results
  3. Return results
```

This O(n) scan is acceptable because:
- Typical operators manage 10^3 to 10^5 active leases.
- BadgerDB's LSM-tree provides sub-millisecond single-record reads.
- Iterator prefetch (`PrefetchSize: 10`) amortizes I/O cost.
- For production scale, secondary indexes can be added via a separate index namespace.

### 6.4 Concurrency Control

The `BadgerStore` wraps all BadgerDB operations with Go `sync.RWMutex`:
- **Read operations** (`Get*`, `List*`): Acquire `RLock`, allowing concurrent reads.
- **Write operations** (`Create*`, `Update*`, `Delete*`): Acquire `Lock`, serializing writers.
- BadgerDB itself supports ACID transactions (`db.Update`, `db.View`) at the KV level.

---

## 7. Federation Protocol Design

### 7.1 Gossip Mechanism

The federation layer uses a **periodic pull-push gossip protocol** for state synchronization:

```
+---------------------------------------------------------------------+
|                     GOSSIP ROUND (per interval)                      |
+---------------------------------------------------------------------+
|                                                                      |
|  Step 1: SYNC (pull)                                                 |
|  For each active peer (concurrent):                                  |
|    a. GET /v1/commitments/<peer>/<latest> → store commitment         |
|    b. GET /v1/leases?status=ACTIVE&operator_id=<peer> → cache leases |
|    c. Update peer LastSeen timestamp                                 |
|                                                                      |
|  Step 2: DETECT (analyze)                                            |
|    a. Compare local active leases with all cached peer leases        |
|    b. For each (local, remote) pair:                                 |
|       - If same wavelength + overlapping time → DOUBLE_ALLOCATION    |
|    c. For each cached lease:                                         |
|       - If EndTime < Now + status=ACTIVE → EXPIRED_LEASE             |
|    d. Return list of ProofOfInvalidity structs                       |
|                                                                      |
|  Step 3: PUSH (propagate)                                            |
|    a. Build local Merkle commitment                                  |
|    b. POST /v1/commitments to all active peers (best-effort)         |
|                                                                      |
+---------------------------------------------------------------------+
```

**Gossip Interval**: Configurable, default 30 seconds.

**Concurrency Model**: The sync step fans out goroutines for all peers concurrently, collecting results via a buffered error channel. This ensures that a slow peer does not block synchronization with others.

### 7.2 Conflict Detection Algorithm

```
findConflicts(localLeases, remoteLeases):

  Phase 1: Expired lease detection
    for each lease in localLeases:
      if lease.EndTime < Now AND lease.Status == ACTIVE:
        emit EXPIRED_LEASE conflict

  Phase 2: Cross-operator double-allocation
    for each local in localLeases:
      for each remote in remoteLeases:
        if local.Status != ACTIVE or remote.Status != ACTIVE: skip
        if local.EndTime < Now or remote.EndTime < Now: skip
        if local.Wavelength.Key != remote.Wavelength.Key: skip
        if time ranges overlap:
          emit DOUBLE_ALLOCATION conflict(local, remote)
```

Time overlap check: `!(A.EndTime < B.StartTime || B.EndTime < A.StartTime)`

### 7.3 Translation Tables

Cross-domain wavelength routing at AWG junctions requires pre-negotiated translation tables:

```
Operator A (lambda 1550.12nm) -----> AWG Junction J1 -----> Operator B (lambda 1551.72nm)
                                    Input Port: 3           Output Port: 7
                                    
TranslationEntry:
  FromOperator: "op-a"     ToOperator: "op-b"
  FromWavelength: 1550.12nm  ToWavelength: 1551.72nm
  FromAWGPort: 3            ToAWGPort: 7
  Status: ACTIVE
  EffectiveTime: now        ExpiryTime: now + 30 days
```

The `xlat.Manager` validates translations against existing active leases to prevent routing conflicts. The `GenerateAWGTable()` method compiles all active translations for a junction into a vendor-neutral JSON routing table suitable for direct AWG provisioning.

---

## 8. API Design

### 8.1 gRPC Service Definition

```protobuf
service FederatedRegistry {
    // Lease lifecycle
    rpc CreateLease(CreateLeaseRequest) returns (Lease);
    rpc GetLease(GetLeaseRequest) returns (Lease);
    rpc RenewLease(RenewLeaseRequest) returns (Lease);
    rpc RevokeLease(RevokeLeaseRequest) returns (Lease);
    rpc ListLeases(ListLeasesRequest) returns (ListLeasesResponse);

    // Merkle commitment & verification
    rpc GetMerkleCommitment(GetMerkleCommitmentRequest) returns (MerkleCommitment);
    rpc VerifyLease(VerifyLeaseRequest) returns (VerificationResult);
    rpc SubmitProofOfInvalidity(SubmitProofOfInvalidityRequest) returns (InvalidityResult);

    // Operator management
    rpc RegisterOperator(RegisterOperatorRequest) returns (Operator);
    rpc GetOperator(GetOperatorRequest) returns (Operator);
    rpc ListOperators(ListOperatorsRequest) returns (ListOperatorsResponse);

    // Translation tables
    rpc CreateTranslation(CreateTranslationRequest) returns (TranslationEntry);
    rpc GetTranslation(GetTranslationRequest) returns (TranslationEntry);
    rpc ListTranslations(ListTranslationsRequest) returns (ListTranslationsResponse);

    // Streaming updates
    rpc StreamRegistryUpdates(StreamRegistryUpdatesRequest) returns (stream RegistryUpdate);
}
```

### 8.2 REST Mapping (via grpc-gateway)

| gRPC Method | HTTP Method | Path |
|-------------|-------------|------|
| `CreateLease` | POST | `/v1/leases` |
| `GetLease` | GET | `/v1/leases/{lease_id}` |
| `RenewLease` | POST | `/v1/leases/{lease_id}/renew` |
| `RevokeLease` | DELETE | `/v1/leases/{lease_id}` |
| `ListLeases` | GET | `/v1/leases` |
| `GetMerkleCommitment` | GET | `/v1/commitments/{operator_id}/{block_height}` |
| `VerifyLease` | POST | `/v1/verify` |
| `SubmitProofOfInvalidity` | POST | `/v1/invalidity` |
| `RegisterOperator` | POST | `/v1/operators` |
| `GetOperator` | GET | `/v1/operators/{operator_id}` |
| `ListOperators` | GET | `/v1/operators` |
| `CreateTranslation` | POST | `/v1/translations` |
| `GetTranslation` | GET | `/v1/translations/{translation_id}` |
| `ListTranslations` | GET | `/v1/translations` |
| `StreamRegistryUpdates` | GET | `/v1/stream` (SSE) |

### 8.3 gRPC Interceptors

```
Request Flow:
+------------+   +-----------+   +----------+   +----------+   +-----------+
|   Client   |-->|  Logging  |-->|   Auth   |-->| Rate Lmt |-->| Recovery  |--> Handler
+------------+   +-----------+   +----------+   +----------+   +-----------+

1. LoggingInterceptor:
   - Records method name, start time, duration, status code
   - Structured JSON logging via slog

2. AuthInterceptor (mTLS):
   - Extracts peer.TLSInfo from context
   - Verifies client certificate chain
   - Optional OU (OrganizationalUnit) whitelist check
   - Returns codes.Unauthenticated or codes.PermissionDenied

3. RateLimitInterceptor:
   - Token bucket algorithm
   - Configurable max requests per second
   - Returns codes.ResourceExhausted when exceeded

4. RecoveryInterceptor:
   - Deferred panic recovery
   - Logs panic with stack context
   - Returns codes.Internal to client (sanitized)
```

### 8.4 Streaming

The `StreamRegistryUpdates` endpoint uses **gRPC server streaming** (with HTTP/1.1 fallback via SSE). It maintains a persistent connection that sends:
- Initial connection acknowledgment
- Periodic heartbeat messages (30s interval)
- Registry update events (lease created, renewed, revoked, commitment published)

---

## 9. Audit & Compliance Design

### 9.1 ITU-T Validation

The audit system enforces compliance with **ITU-T G.694.1** DWDM grid specifications:

```
ValidateITUCompliance(wavelength):
  1. ValidateBand(lambdaNm, band):
     - C_BAND: 1530.0 nm <= lambda <= 1565.0 nm
     - L_BAND: 1565.0 nm <= lambda <= 1625.0 nm
     - S_BAND: 1460.0 nm <= lambda <= 1530.0 nm

  2. Validate grid spacing:
     - Supported: 12.5, 25.0, 50.0, 100.0 GHz
     - Check |spacing - nominal| < 0.01 GHz

  3. Validate channel number:
     - Compute expected channel from wavelength and grid
     - Formula: channel = round((freq - 193.1THz) / (gridGHz/1000))
     - Verify matches stored ChannelNum
```

**Physical Constants**:
- Speed of light: 2.99792458e8 m/s (exact, SI definition)
- ITU-T reference wavelength: 1552.524 nm (193.1 THz)

### 9.2 Hash Chain Integrity

The audit log forms a **cryptographic hash chain**:

```
Entry 1:  hash = SHA3-256(op + "CREATE_LEASE" + lease_id + details)
          prev_hash = [all zeros]

Entry 2:  hash = SHA3-256(op + "RENEW_LEASE" + lease_id + details)
          prev_hash = Entry 1.hash

Entry 3:  hash = SHA3-256(op + "REVOKE_LEASE" + lease_id + details)
          prev_hash = Entry 2.hash

Tamper Detection:
  If Entry 2 is modified → Entry 2.hash changes
  → Entry 3.prev_hash no longer matches
  → Chain integrity check fails
```

The `CheckLeaseChainIntegrity()` method verifies this chain by sorting all leases by `CreatedAt` and checking that each `ParentHash` matches the SHA3-256 of the previous lease's canonical representation.

### 9.3 Audit Log Structure

```
+-------------------------------------------------------------------+
|                     AUDIT LOG ENTRY                               |
+-------------------------------------------------------------------+
|  Timestamp  : RFC3339Nano — when the event occurred               |
|  Operation  : CREATE_LEASE | RENEW_LEASE | REVOKE_LEASE |         |
|               COMMIT_MERKLE | EXPIRE_LEASE | PROOF_OF_INVALIDITY  |
|  OperatorID : The operator performing the action                  |
|  LeaseID    : Target lease (empty for non-lease operations)       |
|  Details    : JSON-encoded context (block height, etc.)           |
|  Hash       : SHA3-256 of this entry's canonical form             |
|  PrevHash   : SHA3-256 of the previous entry (hash chain link)    |
+-------------------------------------------------------------------+
```

### 9.4 Audit Report Generation

The `GenerateAuditReport(from, to)` method produces a comprehensive compliance summary:

```
AuditReport:
  - Total/Active/Expired/Revoked lease counts
  - Total/Active operator counts
  - Merkle commitment count
  - Hash chain validity flag
  - ComplianceViolations array:
    * EXPIRED_LEASE_ACTIVE (severity: high)
    * ITU_COMPLIANCE (severity: medium)
    * HASH_CHAIN_BROKEN (severity: critical)
```

---

## 10. Chaincode Design

### 10.1 Hyperledger Fabric Integration

The FLR chaincode provides a **tamper-evident, consensus-backed** layer for cross-operator commitment verification. It runs as a Hyperledger Fabric v2.x Go chaincode using the `fabric-contract-api-go` framework.

```
+-------------------------------------------------------------------+
|                    ON-CHAIN DATA MODEL                             |
+-------------------------------------------------------------------+
|                                                                    |
|  Key: commitment:<op_id>:<height>                                  |
|  Value: CommitmentRecord {                                         |
|    operator_id, root_hash, lease_count, block_height,              |
|    timestamp, signature                                            |
|  }                                                                 |
|                                                                    |
|  Key: commitment:latest:<op_id>                                    |
|  Value: <latest CommitmentRecord>                                  |
|                                                                    |
|  Key: history:<op_id>                                              |
|  Value: [CommitmentRecord, ...]                                    |
|                                                                    |
|  Key: poi:<poi_id>                                                 |
|  Value: ProofOfInvalidityRecord {                                  |
|    poi_id, type, lease_a_id, lease_b_id,                           |
|    operator_id, merkle_proof[], timestamp                          |
|  }                                                                 |
|                                                                    |
|  Key: operator:<op_id>                                             |
|  Value: OperatorRecord { id, name, public_key, endpoint,           |
|    status, joined_at, last_seen }                                  |
|                                                                    |
|  Key: lease:<lease_id>                                             |
|  Value: LeaseRecord { lease_id, operator_id, wavelength_key,       |
|    endpoint_id, status, token_hash, start_time, end_time,          |
|    block_height, timestamp }                                       |
|                                                                    |
|  Key: leases:<operator_id>                                         |
|  Value: [lease_id, ...]                                            |
|                                                                    |
|  Counters: operator_count, lease_count, commitment_count, poi_count|
+-------------------------------------------------------------------+
```

### 10.2 Chaincode Transaction Functions

| Function | Type | Purpose |
|----------|------|---------|
| `InitLedger` | Init | Initialize counter state to zero |
| `SubmitCommitment` | Submit | Store signed commitment; update latest pointer; append to history |
| `GetCommitment` | Query | Retrieve commitment by operator and height |
| `GetLatestCommitment` | Query | Retrieve most recent commitment for operator |
| `VerifyLease` | Query | Check if lease hash exists in operator's state |
| `SubmitProofOfInvalidity` | Submit | Record a PoI with auto-generated ID |
| `GetProofOfInvalidity` | Query | Retrieve a PoI by ID |
| `GetOperatorHistory` | Query | Get all commitments for an operator |
| `RegisterOperator` | Submit | Register operator with deduplication check |
| `GetOperator` | Query | Get operator by ID |
| `ListOperators` | Query | Range scan all operators |
| `RecordLease` | Submit | Store lease record with auto block height |
| `UpdateLeaseStatus` | Submit | Update lease status with validation |
| `ListActiveLeases` | Query | Get all active leases for an operator |

### 10.3 On-Chain vs. Off-Chain Data Flow

```
Local Node                                  Hyperledger Fabric
+----------+                               +------------------+
| Registry |-- 1. Build Merkle tree ----->| Chaincode        |
| Engine   |                              | SubmitCommitment |
+----------+                               +--------+---------+
     |                                              |
     | 2. Sign commitment                           | 3. Consensus
     |    (ECDSA P-256)                             |    (RAFT/BFT)
     |                                              |
     v                                              v
+----------+                               +------------------+
| Crypto   |<-- 4. Verifiable proof --------| Distributed      |
| Engine   |    (any peer can verify)       | Ledger           |
+----------+                               +------------------+
```

---

## 11. Security Model

### 11.1 Threat Model

| Threat | Severity | Mitigation |
|--------|----------|------------|
| **Double-allocation attack** | Critical | Pessimistic conflict check at allocation time; cross-operator detection via gossip |
| **Replay attack** | High | 32-byte random nonce in every token; tokens include lease ID and timestamps |
| **Man-in-the-middle (federation)** | High | mTLS with client certificate verification; ECDSA signature on all commitments |
| **Tampered audit log** | High | Hash chain with SHA3-256; each entry links to previous via PrevHash |
| **Unauthorized operator** | Medium | Operator registration requires existing federation member approval; on-chain operator records |
| **Rate limit exhaustion** | Medium | Token bucket rate limiter on gRPC interceptors; configurable max connections |
| **Panic/crash exploitation** | Medium | Recovery interceptor catches panics; sanitized error messages to clients |
| **Stale data read** | Low | Read-write mutex on registry engine; atomic commitment storage |

### 11.2 mTLS Authentication

```
+------------------------------------------------------------------+
|                    mTLS HANDSHAKE FLOW                            |
+------------------------------------------------------------------+
|                                                                    |
|  Client (Operator A)          Server (Operator B)                  |
|  +----------------+           +----------------+                   |
|  | Client Cert    |           | Server Cert    |                   |
|  | (signed by CA) |           | (signed by CA) |                   |
|  | + Private Key  |           | + Private Key  |                   |
|  +----------------+           +----------------+                   |
|         |                              |                           |
|         |------ TLS Handshake -------->|                           |
|         |  1. Client presents cert     |                           |
|         |  2. Server verifies cert     |                           |
|         |  3. Server presents cert     |                           |
|         |  4. Client verifies cert     |                           |
|         |<----- Encrypted Channel -----|                           |
|                                                                    |
|  gRPC authInterceptor:                                             |
|    - Extract peer.TLSInfo from context                             |
|    - Verify certificate chain against ClientCA                     |
|    - Optional OU whitelist check                                   |
|    - Reject with codes.Unauthenticated if any check fails          |
+------------------------------------------------------------------+
```

### 11.3 Token Security Properties

| Property | Mechanism |
|----------|-----------|
| **Authenticity** | ECDSA P-256 signature over canonical JSON |
| **Integrity** | SHA3-256 hash stored on-chain; tampering invalidates hash |
| **Non-replayability** | 32-byte random nonce ensures unique tokens per issuance |
| **Time-bound validity** | Explicit StartTime/EndTime in token; validated server-side |
| **Issuer binding** | OperatorID embedded in token; verified against known operators |

---

## 12. Performance Considerations

### 12.1 Expected Throughput

| Operation | Expected Latency | Expected Throughput |
|-----------|-----------------|---------------------|
| Lease creation | < 10 ms (local) | 1,000 ops/s per node |
| Lease renewal | < 5 ms | 2,000 ops/s per node |
| Merkle tree build (1K leases) | < 50 ms | 20 trees/s |
| Merkle proof generation | < 1 ms | 10,000 proofs/s |
| Token validation | < 2 ms | 5,000 validations/s |
| Gossip round (3 peers) | < 500 ms | 2 rounds/s |
| Audit report generation | < 100 ms | 10 reports/s |

### 12.2 Scalability Characteristics

```
Dimension               Growth Model       Bottleneck
+-----------------------+------------------+------------------------+
| Local leases          | O(n) storage     | BadgerDB L0 compaction |
| Federation peers      | O(p) connections | Network I/O, goroutines|
| Merkle tree depth     | O(log n)         | Memory (tree in-mem)   |
| Gossip payload        | O(active leases) | Bandwidth              |
| Audit log             | O(operations)    | Disk space             |
+-----------------------+------------------+------------------------+
```

**Merkle Tree Rebuilding**: The tree is rebuilt from scratch on each commitment cycle. With 10,000 leases, this involves ~20,000 hash operations (SHA3-256), completing in under 50ms on modern hardware. For larger registries, incremental Merkle trees can be adopted.

### 12.3 Memory Footprint

| Component | Per-Instance Memory |
|-----------|-------------------|
| BadgerDB (empty) | ~32 MB |
| Per 1,000 leases | ~2 MB |
| Merkle tree (1K leaves) | ~512 KB |
| Federation peer cache | ~10 KB per peer |
| gRPC server | ~5 MB base |

---

## 13. Failure Handling

### 13.1 Graceful Degradation

```
+------------------------------------------------------------------+
|                 FAILURE SCENARIOS & RESPONSES                     |
+------------------------------------------------------------------+
|                                                                    |
|  Scenario                    | Behavior                            |
|  ----------------------------|-------------------------------------|
|  Single peer unreachable     | Log warning; continue with others   |
|  All peers unreachable       | Local operations continue; retry    |
|                              | on next gossip round                |
|  BadgerDB corruption         | Return error; node requires manual  |
|                              | intervention and recovery           |
|  Private key unavailable     | Node starts in read-only mode; no   |
|                              | lease ops or commitments possible   |
|  gRPC panic in handler       | Recovery interceptor catches;       |
|                              | returns codes.Internal to client    |
|  Rate limit exceeded         | Returns codes.ResourceExhausted;    |
|                              | client should back off              |
|  Invalid peer commitment     | Reject and flag; attempt PoI        |
|                              | submission                          |
|  Clock skew > 1s             | Log warning; use NTP; timestamps    |
|                              | include timezone info (UTC)         |
+------------------------------------------------------------------+
```

### 13.2 Error Handling Strategy

The FLR uses a **structured error propagation** model:

1. **Internal errors**: Wrapped with context using `fmt.Errorf("...: %w", err)` to preserve error chains.
2. **gRPC status codes**: Mapped to appropriate `google.golang.org/grpc/codes`:
   - `InvalidArgument` — malformed requests
   - `NotFound` — missing resources
   - `Internal` — server-side failures
   - `Unauthenticated` — auth failures
   - `PermissionDenied` — authorization failures
   - `ResourceExhausted` — rate limiting
3. **Non-fatal errors**: Audit logging failures are logged but do not fail the primary operation.
4. **Federation errors**: Individual peer failures are isolated; one failed sync does not abort the entire gossip round.

### 13.3 Data Recovery

| Scenario | Recovery Action |
|----------|----------------|
| Node restart | BadgerDB reopens; state fully recovered from disk |
| Corrupted lease record | Audit log can reconstruct history; chain integrity check identifies gap |
| Lost private key | Operator must generate new keypair and re-register with federation |
| Blockchain desync | Replay all commitments from chaincode history |

---

## 14. Deployment Architecture

### 14.1 3-Node Federation Topology

```
+=======================================================================+
|                      3-NODE FEDERATION NETWORK                         |
+=======================================================================+
|                                                                        |
|    +-------------------+        +-------------------+                 |
|    |   Operator Node A  |<------>|   Operator Node B  |                 |
|    |   (op-001)         |  mTLS  |   (op-002)         |                 |
|    |   0.0.0.0:9090    |        |   0.0.0.0:9090    |                 |
|    |   0.0.0.0:8080    |        |   0.0.0.0:8080    |                 |
|    +--------+----------+        +--------+----------+                 |
|             |    ^                       |    ^                        |
|             |    | mTLS gossip           |    | mTLS gossip            |
|             v    |                       v    |                        |
|    +--------+----------+        +--------+----------+                 |
|    |   Operator Node C  |<------>|  AWG Junctions     |                 |
|    |   (op-003)         |  mTLS  |  (passive optical) |                 |
|    |   0.0.0.0:9090    |        |                    |                 |
|    |   0.0.0.0:8080    |        |  Pre-provisioned   |                 |
|    +-------------------+        |  translation tables|                 |
|                                 +-------------------+                 |
|                                                                        |
|  Hyperledger Fabric (optional):                                       |
|  +---------------------------------------------------------------+    |
|  |  Orderer | Peer-A (op-001) | Peer-B (op-002) | Peer-C (op-003) |   |
|  +---------------------------------------------------------------+    |
+=======================================================================+
```

### 14.2 Docker Deployment

```yaml
# docker-compose.yml (simplified)
services:
  flr-node-a:
    image: flr:latest
    ports:
      - "9090:9090"   # gRPC
      - "8080:8080"   # REST
    volumes:
      - ./data/op-a:/data/registry
      - ./keys/op-a:/keys
      - ./certs:/certs
    environment:
      - FLR_NODE_ID=op-001
      - FLR_FEDERATION_GOSSIP_INTERVAL=30s
    networks:
      - flr-federation

  flr-node-b:
    image: flr:latest
    ports:
      - "9091:9090"
      - "8081:8080"
    volumes:
      - ./data/op-b:/data/registry
      - ./keys/op-b:/keys
      - ./certs:/certs
    environment:
      - FLR_NODE_ID=op-002
    networks:
      - flr-federation

  flr-node-c:
    image: flr:latest
    ports:
      - "9092:9090"
      - "8082:8080"
    volumes:
      - ./data/op-c:/data/registry
      - ./keys/op-c:/keys
      - ./certs:/certs
    environment:
      - FLR_NODE_ID=op-003
    networks:
      - flr-federation

networks:
  flr-federation:
    driver: bridge
```

### 14.3 Monitoring and Observability

| Metric | Collection Method | Alert Threshold |
|--------|-------------------|-----------------|
| gRPC request rate | Logging interceptor | > 10K req/s |
| gRPC error rate | Logging interceptor | > 1% errors |
| Gossip round duration | Structured log | > 5 seconds |
| Active lease count | Audit report | Capacity planning |
| Conflict detection count | Federation log | > 0 (immediate) |
| BadgerDB LSM compaction | Badger metrics | > 100ms pause |
| mTLS handshake failures | Auth interceptor | > 5 failures/min |

**Health Check Endpoint**: `GET /health` returns 200 OK when the node is operational, including:
- BadgerDB connectivity status
- Number of registered operators
- Latest block height
- Federation peer reachability summary

### 14.4 Key Directories

```
/etc/flr/
├── config.yaml          # Node configuration
├── certs/
│   ├── server.crt       # TLS server certificate
│   ├── server.key       # TLS server private key
│   └── ca.crt           # Client CA certificate (for mTLS)
└── keys/
    ├── node.pem         # ECDSA P-256 private key (PKCS#8)
    └── node.pub         # Public key (PKIX PEM)

/var/lib/flr/
└── data/
    └── registry/        # BadgerDB data directory
        ├── MANIFEST
        ├── 00001.vlog
        └── 00001.sst

/var/log/flr/
└── flr.log              # Structured JSON logs
```

---

## Appendix A: Glossary

| Term | Definition |
|------|------------|
| **Lambda (lambda)** | Wavelength of light in an optical fiber, measured in nanometers (nm) |
| **OTAP** | Optical Transport Access Point — a network demarcation point |
| **AWG** | Arrayed Waveguide Grating — passive optical device for wavelength multiplexing |
| **DWDM** | Dense Wavelength Division Multiplexing — technology for multiple lambdas on one fiber |
| **ITU-T G.694.1** | International standard defining DWDM grid frequencies and channel numbering |
| **Merkle Tree** | Binary hash tree enabling efficient verification of data integrity |
| **Merkle Proof** | Sibling path from leaf to root enabling cryptographic inclusion verification |
| **PoI** | Proof of Invalidity — cryptographic evidence of a registry violation |
| **LSM-tree** | Log-Structured Merge tree — write-optimized storage structure |

## Appendix B: File Layout

```
/mnt/agents/output/flr/
├── cmd/flr/                    # CLI application
│   ├── main.go
│   ├── init.go
│   ├── lease.go
│   ├── operator.go
│   ├── endpoint.go
│   ├── commit.go
│   ├── xlat.go
│   ├── audit.go
│   ├── serve.go
│   ├── status.go
│   └── config_cmd.go
├── internal/
│   ├── api/                    # gRPC + REST server
│   │   ├── server.go
│   │   └── interceptor.go
│   ├── audit/                  # Compliance & audit
│   │   ├── auditor.go
│   │   └── compliance.go
│   ├── config/                 # Configuration
│   │   └── config.go
│   ├── crypto/                 # Cryptographic engine
│   │   └── engine.go
│   ├── federation/             # Cross-operator federation
│   │   ├── manager.go
│   │   ├── client.go
│   │   ├── gossip.go
│   │   └── conflict.go
│   ├── models/                 # Core data models
│   │   └── models.go
│   ├── registry/               # Registry engine & storage
│   │   ├── engine.go
│   │   ├── store.go
│   │   └── badger_store.go
│   └── xlat/                   # Translation tables
│       └── manager.go
├── contracts/                  # Hyperledger Fabric chaincode
│   └── chaincode.go
└── proto/flr/v1/               # Protocol Buffer schemas
    └── registry.proto
```

---

*End of Design Document*
