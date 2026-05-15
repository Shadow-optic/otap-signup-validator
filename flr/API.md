# Federated Lambda Registry (FLR) API Reference

> **Version**: 1.0.0  
> **Protocol**: gRPC + REST (HTTP/1.1 & HTTP/2)  
> **Content Type**: `application/json` (REST) / `application/grpc` (gRPC)  
> **Updated**: 2025-06-24

---

## Table of Contents

1. [API Overview](#1-api-overview)
2. [Base URLs](#2-base-urls)
3. [Authentication](#3-authentication)
4. [Error Codes](#4-error-codes)
5. [Rate Limiting](#5-rate-limiting)
6. [Complete Endpoint Reference](#6-complete-endpoint-reference)
7. [Data Types](#7-data-types)
8. [Streaming API](#8-streaming-api)
9. [Error Format](#9-error-format)
10. [Pagination](#10-pagination)
11. [SDK Example (Go)](#11-sdk-example-go)
12. [curl Examples](#12-curl-examples)

---

## 1. API Overview

The Federated Lambda Registry (FLR) provides a dual-protocol API surface supporting both gRPC (HTTP/2) and REST (HTTP/1.1 with JSON). The API manages wavelength leases, operator registrations, cross-operator wavelength translations, and cryptographic commitments via Merkle tree roots. It is designed for optical transport network operators participating in a federated trust model.

```
+-------------+    gRPC/HTTP2    +-------------------------+
|   Client    | ---------------> |  grpc.flr.otap.network  |
|  (SDK/lib)  | :9090 (mtls)    |    (Federated Registry)  |
+-------------+                  +-------------------------+
                                    | REST/HTTP1.1
                                    v
                               +-------------------------+
                               |  api.flr.otap.network   |
                               | :8080/v1/ (mtls+json) |
                               +-------------------------+
```

### Design Principles

| Principle | Description |
|-----------|-------------|
| **Dual Protocol** | Every gRPC method is exposed via REST with JSON request/response bodies. |
| **Mutual TLS** | All connections require client certificate authentication; no bearer tokens or API keys. |
| **Cryptographic Integrity** | Lease tokens are signed; Merkle commitments provide tamper-evident audit trails. |
| **Real-Time Streaming** | Server-Sent Events (SSE) deliver live registry updates for event-driven consumers. |
| **Deterministic IDs** | All resources carry stable string identifiers suitable for caching and deduplication. |

---

## 2. Base URLs

### REST (HTTP/1.1 + JSON)

```
https://api.flr.otap.network:8080/v1/
```

All REST endpoints are prefixed with `/v1/`. Requests must set `Content-Type: application/json`. Responses are always `application/json` unless an error occurs, in which case `application/problem+json` may be returned.

### gRPC (HTTP/2 + Protobuf)

```
grpc.flr.otap.network:9090
```

The gRPC service name is `flr.v1.FederatedRegistry`. Use the `grpc` package in your language of choice with the generated stubs from `proto/flr/v1/registry.proto`.

### Health Check

```
GET /v1/health
```

Returns `{"status": "SERVING"}` when the registry is healthy.

---

## 3. Authentication

FLR enforces **mutual TLS (mTLS)** for all transport layers. Both REST and gRPC endpoints require a valid client certificate signed by a Certificate Authority (CA) trusted by the FLR federation.

### Certificate Requirements

| Requirement | Description |
|-------------|-------------|
| **CA** | Client certificate must be signed by a CA trusted by the FLR federation root store. |
| **Common Name (CN)** | Must match the registered `operator_id` in the registry. |
| **Subject Alternative Name** | Optional; if present, must include the operator's DNS endpoint. |
| **Key Algorithm** | ECDSA P-256 or RSA 2048-bit minimum. |
| **Expiry** | Certificates with < 24 hours validity are rejected. |

### Configuring mTLS (curl)

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt \
     --key operator.key \
     https://api.flr.otap.network:8080/v1/leases
```

### Configuring mTLS (Go gRPC)

```go
cert, _ := tls.LoadX509KeyPair("operator.crt", "operator.key")
caCert, _ := os.ReadFile("ca-chain.pem")
caPool := x509.NewCertPool()
caPool.AppendCertsFromPEM(caCert)

tlsConfig := &tls.Config{
    Certificates:       []tls.Certificate{cert},
    RootCAs:            caPool,
    InsecureSkipVerify: false,
}
conn, err := grpc.Dial("grpc.flr.otap.network:9090",
    grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
```

### Certificate Rotation

Operators should rotate certificates **before** expiry. The registry caches the operator identity derived from the CN for the duration of the connection. If your certificate changes mid-connection, reconnect to establish a new TLS session.

---

## 4. Error Codes

All errors follow the [gRPC status code](https://grpc.github.io/grpc/core/md_doc_statuscodes.html) conventions, mapped to HTTP status codes for REST consumers.

| gRPC Code | HTTP Status | Meaning | Typical Causes |
|-----------|-------------|---------|----------------|
| `OK` | 200 | Success | Request completed normally. |
| `CANCELLED` | 499 | Client cancelled | Client closed connection before server finished. |
| `INVALID_ARGUMENT` | 400 | Bad request | Missing required fields, malformed JSON, invalid enum values. |
| `FAILED_PRECONDITION` | 409 | Conflict | Lease overlap, operator already registered, wavelength unavailable. |
| `NOT_FOUND` | 404 | Not found | Lease, operator, translation, or commitment does not exist. |
| `ALREADY_EXISTS` | 409 | Already exists | Duplicate registration for an operator ID or lease ID collision. |
| `PERMISSION_DENIED` | 403 | Forbidden | Client certificate CN does not match the requested operator. |
| `UNAUTHENTICATED` | 401 | Unauthorized | Missing, expired, or invalid client certificate. |
| `RESOURCE_EXHAUSTED` | 429 | Too many requests | Rate limit exceeded; see `Retry-After` header. |
| `UNAVAILABLE` | 503 | Service unavailable | Registry is temporarily unavailable; retry with backoff. |
| `INTERNAL` | 500 | Internal error | Unexpected server error; contact federation operators. |

### Error Response Format

```json
{
  "code": "NOT_FOUND",
  "message": "lease 'lease-42-c-band' not found in registry",
  "details": {
    "resource_type": "Lease",
    "resource_id": "lease-42-c-band"
  }
}
```

---

## 5. Rate Limiting

Rate limiting is applied per-operator, identified by the client certificate's Common Name. All endpoints in aggregate count toward the operator's limit.

### Default Limits

| Tier | Limit | Burst | Window |
|------|-------|-------|--------|
| **Standard Operator** | 120 requests/minute | 20 | Sliding window |
| **Relay Node** | 600 requests/minute | 100 | Sliding window |
| **Read-Only Queries** | 300 requests/minute | 50 | Sliding window |

### Rate Limit Headers (REST)

Every REST response includes the following headers:

| Header | Description |
|--------|-------------|
| `X-RateLimit-Limit` | Maximum requests per window. |
| `X-RateLimit-Remaining` | Remaining requests in current window. |
| `X-RateLimit-Reset` | Unix timestamp when the window resets. |
| `X-RateLimit-Retry-After` | Seconds until retry is permitted (present only when rate limited). |

When rate limited, the API returns HTTP 429 with:

```json
{
  "code": "RESOURCE_EXHAUSTED",
  "message": "rate limit exceeded for operator 'operator-alpha'",
  "details": {
    "retry_after_seconds": 45,
    "limit": 120,
    "window": "60s"
  }
}
```

---

## 6. Complete Endpoint Reference

### 6.1 CreateLease

Allocate a new wavelength lease on a specific endpoint.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.CreateLease` |
| **REST Path** | `POST /v1/leases` |
| **Request Type** | `CreateLeaseRequest` |
| **Response Type** | `Lease` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `wavelength` | `Wavelength` | Yes | The wavelength to lease (lambda, channel, band, grid spacing). |
| `endpoint_id` | `string` | Yes | Target endpoint identifier. |
| `operator_id` | `string` | Yes | Operator making the request; must match TLS CN. |
| `start_time` | `Timestamp` | Yes | RFC 3339 timestamp when the lease begins. |
| `duration` | `Duration` | Yes | Lease duration as a protobuf duration string (e.g., `"3600s"`). |

#### Response Fields

Returns a full `Lease` object (see [Data Types](#7-data-types)).

#### Example Request (REST)

```json
POST /v1/leases HTTP/1.1
Host: api.flr.otap.network:8080
Content-Type: application/json

{
  "wavelength": {
    "lambda_nm": 1550.12,
    "channel_num": 42,
    "band": "BAND_C_BAND",
    "grid_ghz": 50.0
  },
  "endpoint_id": "endpoint-nyc-01",
  "operator_id": "operator-alpha",
  "start_time": "2025-06-24T10:00:00Z",
  "duration": "3600s"
}
```

#### Example Response

```json
{
  "id": "lease-42-c-band-20250624",
  "wavelength": {
    "lambda_nm": 1550.12,
    "channel_num": 42,
    "band": "BAND_C_BAND",
    "grid_ghz": 50.0
  },
  "endpoint": {
    "id": "endpoint-nyc-01",
    "node_id": "node-nyc-east",
    "operator_id": "operator-alpha",
    "address": "192.0.2.100:5000",
    "awg_port": 7,
    "coordinates": {"lat": 40.7128, "long": -74.006},
    "status": "ENDPOINT_ACTIVE",
    "created_at": "2024-01-15T08:00:00Z"
  },
  "operator_id": "operator-alpha",
  "status": "LEASE_ACTIVE",
  "start_time": "2025-06-24T10:00:00Z",
  "end_time": "2025-06-24T11:00:00Z",
  "created_at": "2025-06-24T09:59:30Z",
  "updated_at": "2025-06-24T09:59:30Z",
  "token_hash": "aGVsbG8g...",
  "parent_hash": "d29ybGQg..."
}
```

#### Error Cases

- `INVALID_ARGUMENT` (400) -- Missing `wavelength`, `endpoint_id`, or `operator_id`.
- `FAILED_PRECONDITION` (409) -- Requested wavelength is already allocated on the endpoint.
- `NOT_FOUND` (404) -- Specified `endpoint_id` does not exist.
- `PERMISSION_DENIED` (403) -- `operator_id` in request does not match the TLS certificate CN.

---

### 6.2 GetLease

Retrieve a single lease by its identifier.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.GetLease` |
| **REST Path** | `GET /v1/leases/{lease_id}` |
| **Request Type** | `GetLeaseRequest` |
| **Response Type** | `Lease` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `lease_id` | `string` | Yes (URL path) | Unique lease identifier. |

#### Example Response

Same structure as `CreateLease` response. Returns the `Lease` object for the given ID.

#### Error Cases

- `NOT_FOUND` (404) -- Lease ID does not exist.
- `INVALID_ARGUMENT` (400) -- `lease_id` is empty.

---

### 6.3 RenewLease

Extend the duration of an active lease.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.RenewLease` |
| **REST Path** | `POST /v1/leases/{lease_id}/renew` |
| **Request Type** | `RenewLeaseRequest` |
| **Response Type** | `Lease` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `lease_id` | `string` | Yes (URL path) | Lease identifier to renew. |
| `extension` | `Duration` | Yes | Additional duration to extend (e.g., `"1800s"`). |

#### Example Request (REST)

```json
POST /v1/leases/lease-42-c-band-20250624/renew HTTP/1.1
Host: api.flr.otap.network:8080
Content-Type: application/json

{
  "extension": "1800s"
}
```

#### Error Cases

- `NOT_FOUND` (404) -- Lease ID does not exist.
- `FAILED_PRECONDITION` (409) -- Lease is not in `LEASE_ACTIVE` status.
- `PERMISSION_DENIED` (403) -- Caller is not the lease owner.

---

### 6.4 RevokeLease

Prematurely terminate an active lease.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.RevokeLease` |
| **REST Path** | `POST /v1/leases/{lease_id}/revoke` |
| **Request Type** | `RevokeLeaseRequest` |
| **Response Type** | `Lease` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `lease_id` | `string` | Yes (URL path) | Lease identifier to revoke. |
| `reason` | `string` | No | Human-readable reason for revocation (audit logging). |

#### Example Request (REST)

```json
POST /v1/leases/lease-42-c-band-20250624/revoke HTTP/1.1
Host: api.flr.otap.network:8080
Content-Type: application/json

{
  "reason": "Maintenance window required for fiber upgrade"
}
```

#### Error Cases

- `NOT_FOUND` (404) -- Lease ID does not exist.
- `FAILED_PRECONDITION` (409) -- Lease already expired or revoked.
- `PERMISSION_DENIED` (403) -- Caller is not the lease owner.

---

### 6.5 ListLeases

List leases with optional filtering.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.ListLeases` |
| **REST Path** | `GET /v1/leases` |
| **Request Type** | `ListLeasesRequest` |
| **Response Type** | `ListLeasesResponse` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operator_id` | `string` | No | Filter to leases owned by this operator. |
| `endpoint_id` | `string` | No | Filter to leases on this endpoint. |
| `wavelength` | `Wavelength` | No | Filter to leases matching this wavelength specification. |
| `status` | `LeaseStatus` | No | Filter by lease status (e.g., `LEASE_ACTIVE`). |

#### Query Parameters (REST)

```
GET /v1/leases?operator_id=operator-alpha&status=LEASE_ACTIVE
```

#### Example Response

```json
{
  "leases": [
    {
      "id": "lease-42-c-band-20250624",
      "wavelength": {
        "lambda_nm": 1550.12,
        "channel_num": 42,
        "band": "BAND_C_BAND",
        "grid_ghz": 50.0
      },
      "endpoint": { ... },
      "operator_id": "operator-alpha",
      "status": "LEASE_ACTIVE",
      "start_time": "2025-06-24T10:00:00Z",
      "end_time": "2025-06-24T11:00:00Z",
      "created_at": "2025-06-24T09:59:30Z",
      "updated_at": "2025-06-24T09:59:30Z",
      "token_hash": "aGVsbG8g...",
      "parent_hash": "d29ybGQg..."
    }
  ],
  "total": 1
}
```

---

### 6.6 GetMerkleCommitment

Retrieve the signed Merkle tree root for an operator at a specific block height.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.GetMerkleCommitment` |
| **REST Path** | `GET /v1/commitments/{operator_id}/{block_height}` |
| **Request Type** | `GetMerkleCommitmentRequest` |
| **Response Type** | `MerkleCommitment` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operator_id` | `string` | Yes (URL path) | Operator identifier. |
| `block_height` | `int64` | Yes (URL path) | Blockchain height for the commitment. |

#### Example Request (REST)

```
GET /v1/commitments/operator-alpha/18446744073709
```

#### Example Response

```json
{
  "operator_id": "operator-alpha",
  "root_hash": "d2f6c7a8b3...",
  "timestamp": "2025-06-24T10:00:00Z",
  "signature": "3045022100abc...",
  "lease_count": 128,
  "block_height": 18446744073709
}
```

#### Error Cases

- `NOT_FOUND` (404) -- No commitment found for the given operator and block height.
- `INVALID_ARGUMENT` (400) -- Negative `block_height` or empty `operator_id`.

---

### 6.7 VerifyLease

Cryptographically verify a lease token against the registry's stored hash.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.VerifyLease` |
| **REST Path** | `POST /v1/verify` |
| **Request Type** | `VerifyLeaseRequest` |
| **Response Type** | `VerificationResult` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `token` | `LeaseToken` | Yes | The signed lease token to verify. |
| `operator_id` | `string` | Yes | The operator that issued the token. |

#### Example Request (REST)

```json
POST /v1/verify HTTP/1.1
Host: api.flr.otap.network:8080
Content-Type: application/json

{
  "token": {
    "version": 1,
    "lease_id": "lease-42-c-band-20250624",
    "operator_id": "operator-alpha",
    "wavelength": {
      "lambda_nm": 1550.12,
      "channel_num": 42,
      "band": "BAND_C_BAND",
      "grid_ghz": 50.0
    },
    "endpoint_id": "endpoint-nyc-01",
    "start_time": "2025-06-24T10:00:00Z",
    "end_time": "2025-06-24T11:00:00Z",
    "nonce": "cmFuZG9tX25vbmNl",
    "signature": "3045022100ef1b...",
    "issued_at": "2025-06-24T09:59:30Z"
  },
  "operator_id": "operator-alpha"
}
```

#### Example Response

```json
{
  "valid": true,
  "reason": "signature and hash match",
  "computed_hash": "aGVsbG8g...",
  "stored_hash": "aGVsbG8g..."
}
```

If invalid:

```json
{
  "valid": false,
  "reason": "signature verification failed: invalid ECDSA signature",
  "computed_hash": "dGhlX2NvcnJlY3RfaGFzaA==",
  "stored_hash": "c29tZV9vdGhlcl9oYXNo"
}
```

#### Error Cases

- `INVALID_ARGUMENT` (400) -- Malformed token or missing fields.
- `NOT_FOUND` (404) -- Referenced lease or operator does not exist.

---

### 6.8 SubmitProofOfInvalidity

Submit cryptographic proof that a registry entry violates federation rules.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.SubmitProofOfInvalidity` |
| **REST Path** | `POST /v1/invalidity` |
| **Request Type** | `SubmitProofOfInvalidityRequest` |
| **Response Type** | `InvalidityResult` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | `string` | Yes | Proof type: `DOUBLE_ALLOCATION`, `EXPIRED_LEASE`, `INVALID_SIGNATURE`, or `UNAUTHORIZED_OPERATION`. |
| `lease_a_id` | `string` | Yes | Primary lease involved in the violation. |
| `lease_b_id` | `string` | Conditional | Secondary lease (required for `DOUBLE_ALLOCATION`). |
| `operator_id` | `string` | Yes | Operator submitting the proof. |
| `merkle_proof` | `repeated bytes` | Yes | Merkle inclusion proof path for verification. |

#### Example Request (REST)

```json
POST /v1/invalidity HTTP/1.1
Host: api.flr.otap.network:8080
Content-Type: application/json

{
  "type": "DOUBLE_ALLOCATION",
  "lease_a_id": "lease-42-c-band-20250624",
  "lease_b_id": "lease-42-c-band-20250624-dup",
  "operator_id": "operator-beta",
  "merkle_proof": [
    "aGVhcjF...",
    "dGhlcmUy...",
    "YW5vdGhlcjMu.."
  ]
}
```

#### Example Response

```json
{
  "accepted": true,
  "resolution": "Violation confirmed. Conflicting lease revoked and operator slashed."
}
```

#### Error Cases

- `INVALID_ARGUMENT` (400) -- Missing or malformed proof.
- `FAILED_PRECONDITION` (409) -- Proof does not verify against stored commitment.
- `UNAUTHENTICATED` (401) -- Invalid client certificate.

---

### 6.9 RegisterOperator

Register a new operator in the federation.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.RegisterOperator` |
| **REST Path** | `POST /v1/operators` |
| **Request Type** | `RegisterOperatorRequest` |
| **Response Type** | `Operator` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | `string` | Yes | Unique operator identifier (must match TLS CN). |
| `name` | `string` | Yes | Human-readable operator name. |
| `public_key` | `bytes` | Yes | Operator's public key (ECDSA P-256 PKIX DER). |
| `endpoint` | `string` | Yes | Network address of the operator's gRPC endpoint. |

#### Example Request (REST)

```json
POST /v1/operators HTTP/1.1
Host: api.flr.otap.network:8080
Content-Type: application/json

{
  "id": "operator-gamma",
  "name": "Gamma Optical Networks",
  "public_key": "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE...",
  "endpoint": "grpc.operator-gamma.example.com:9090"
}
```

#### Example Response

```json
{
  "id": "operator-gamma",
  "name": "Gamma Optical Networks",
  "public_key": "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE...",
  "endpoint": "grpc.operator-gamma.example.com:9090",
  "status": "OPERATOR_ACTIVE",
  "joined_at": "2025-06-24T12:00:00Z",
  "last_seen": "2025-06-24T12:00:00Z"
}
```

#### Error Cases

- `INVALID_ARGUMENT` (400) -- Missing required fields or invalid `public_key` format.
- `ALREADY_EXISTS` (409) -- An operator with this ID is already registered.
- `FAILED_PRECONDITION` (409) -- `id` does not match the TLS certificate CN.

---

### 6.10 GetOperator

Retrieve operator details.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.GetOperator` |
| **REST Path** | `GET /v1/operators/{operator_id}` |
| **Request Type** | `GetOperatorRequest` |
| **Response Type** | `Operator` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operator_id` | `string` | Yes (URL path) | Operator identifier. |

#### Error Cases

- `NOT_FOUND` (404) -- Operator does not exist.
- `INVALID_ARGUMENT` (400) -- Empty `operator_id`.

---

### 6.11 ListOperators

List all registered operators, optionally filtered by status.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.ListOperators` |
| **REST Path** | `GET /v1/operators` |
| **Request Type** | `ListOperatorsRequest` |
| **Response Type** | `ListOperatorsResponse` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `status` | `OperatorStatus` | No | Filter by status (e.g., `OPERATOR_ACTIVE`). |

#### Query Parameters (REST)

```
GET /v1/operators?status=OPERATOR_ACTIVE
```

#### Example Response

```json
{
  "operators": [
    {
      "id": "operator-alpha",
      "name": "Alpha Telecom",
      "public_key": "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE...",
      "endpoint": "grpc.operator-alpha.example.com:9090",
      "status": "OPERATOR_ACTIVE",
      "joined_at": "2024-01-15T08:00:00Z",
      "last_seen": "2025-06-24T14:30:00Z"
    },
    {
      "id": "operator-beta",
      "name": "Beta Fiber Corp",
      "public_key": "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAF...",
      "endpoint": "grpc.operator-beta.example.com:9090",
      "status": "OPERATOR_ACTIVE",
      "joined_at": "2024-03-10T10:00:00Z",
      "last_seen": "2025-06-24T14:28:00Z"
    }
  ]
}
```

---

### 6.12 CreateTranslation

Create a wavelength translation entry mapping wavelengths between two operators.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.CreateTranslation` |
| **REST Path** | `POST /v1/translations` |
| **Request Type** | `CreateTranslationRequest` |
| **Response Type** | `TranslationEntry` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `from_operator` | `string` | Yes | Source operator identifier. |
| `to_operator` | `string` | Yes | Destination operator identifier. |
| `from_wavelength` | `Wavelength` | Yes | Source wavelength specification. |
| `to_wavelength` | `Wavelength` | Yes | Destination wavelength specification. |
| `from_awg_port` | `int32` | Yes | Source AWG port number. |
| `to_awg_port` | `int32` | Yes | Destination AWG port number. |
| `duration` | `Duration` | Yes | Translation validity duration. |

#### Example Request (REST)

```json
POST /v1/translations HTTP/1.1
Host: api.flr.otap.network:8080
Content-Type: application/json

{
  "from_operator": "operator-alpha",
  "to_operator": "operator-beta",
  "from_wavelength": {
    "lambda_nm": 1550.12,
    "channel_num": 42,
    "band": "BAND_C_BAND",
    "grid_ghz": 50.0
  },
  "to_wavelength": {
    "lambda_nm": 1551.72,
    "channel_num": 44,
    "band": "BAND_C_BAND",
    "grid_ghz": 50.0
  },
  "from_awg_port": 7,
  "to_awg_port": 12,
  "duration": "7200s"
}
```

#### Example Response

```json
{
  "id": "trans-alpha-beta-20250624",
  "from_operator": "operator-alpha",
  "to_operator": "operator-beta",
  "from_wavelength": {
    "lambda_nm": 1550.12,
    "channel_num": 42,
    "band": "BAND_C_BAND",
    "grid_ghz": 50.0
  },
  "to_wavelength": {
    "lambda_nm": 1551.72,
    "channel_num": 44,
    "band": "BAND_C_BAND",
    "grid_ghz": 50.0
  },
  "from_awg_port": 7,
  "to_awg_port": 12,
  "status": "TRANSLATION_ACTIVE",
  "effective_time": "2025-06-24T13:00:00Z",
  "expiry_time": "2025-06-24T15:00:00Z"
}
```

#### Error Cases

- `INVALID_ARGUMENT` (400) -- Missing required fields or invalid wavelength specification.
- `FAILED_PRECONDITION` (409) -- Source or destination wavelength/port already in use.
- `NOT_FOUND` (404) -- One of the operators does not exist.

---

### 6.13 GetTranslation

Retrieve a translation entry by ID.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.GetTranslation` |
| **REST Path** | `GET /v1/translations/{translation_id}` |
| **Request Type** | `GetTranslationRequest` |
| **Response Type** | `TranslationEntry` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `translation_id` | `string` | Yes (URL path) | Translation entry identifier. |

#### Error Cases

- `NOT_FOUND` (404) -- Translation does not exist.
- `INVALID_ARGUMENT` (400) -- Empty `translation_id`.

---

### 6.14 ListTranslations

List translation entries with optional filtering.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.ListTranslations` |
| **REST Path** | `GET /v1/translations` |
| **Request Type** | `ListTranslationsRequest` |
| **Response Type** | `ListTranslationsResponse` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `from_operator` | `string` | No | Filter by source operator. |
| `to_operator` | `string` | No | Filter by destination operator. |
| `status` | `TranslationStatus` | No | Filter by translation status. |

#### Query Parameters (REST)

```
GET /v1/translations?from_operator=operator-alpha&status=TRANSLATION_ACTIVE
```

#### Example Response

```json
{
  "translations": [
    {
      "id": "trans-alpha-beta-20250624",
      "from_operator": "operator-alpha",
      "to_operator": "operator-beta",
      "from_wavelength": {
        "lambda_nm": 1550.12,
        "channel_num": 42,
        "band": "BAND_C_BAND",
        "grid_ghz": 50.0
      },
      "to_wavelength": {
        "lambda_nm": 1551.72,
        "channel_num": 44,
        "band": "BAND_C_BAND",
        "grid_ghz": 50.0
      },
      "from_awg_port": 7,
      "to_awg_port": 12,
      "status": "TRANSLATION_ACTIVE",
      "effective_time": "2025-06-24T13:00:00Z",
      "expiry_time": "2025-06-24T15:00:00Z"
    }
  ],
  "total": 1
}
```

---

### 6.15 StreamRegistryUpdates

Subscribe to a real-time stream of registry mutations. This endpoint delivers Server-Sent Events (SSE) over HTTP for REST consumers and a gRPC server-side stream for gRPC consumers.

| Attribute | Value |
|-----------|-------|
| **gRPC Method** | `FederatedRegistry.StreamRegistryUpdates` |
| **REST Path** | `GET /v1/stream` (SSE) |
| **Request Type** | `StreamRegistryUpdatesRequest` |
| **Response Type** | `stream RegistryUpdate` |

#### Request Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operator_id` | `string` | No | Filter to events affecting this operator. |
| `from_block_height` | `int64` | No | Only emit events from this block height onward. |

#### Query Parameters (REST)

```
GET /v1/stream?operator_id=operator-alpha&from_block_height=18446744073700
```

#### SSE Format

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive

event: registry_update
data: {"operation":"LEASE_CREATED","lease":{"id":"lease-42-c-band-20250624",...},"commitment":null,"block_height":18446744073709,"timestamp":"2025-06-24T10:00:00Z"}

event: registry_update
data: {"operation":"MERKLE_COMMITMENT","lease":null,"commitment":{"operator_id":"operator-alpha","root_hash":"d2f6c7a8b3...",...},"block_height":18446744073710,"timestamp":"2025-06-24T10:05:00Z"}
```

See [Streaming API](#8-streaming-api) for full consumer details.

---

## 7. Data Types

### Wavelength

A wavelength specification on the ITU-T optical grid.

| Field | Type | Description |
|-------|------|-------------|
| `lambda_nm` | `double` | Wavelength in nanometers (e.g., `1550.12`). |
| `channel_num` | `int32` | ITU-T channel number (e.g., `42`). |
| `band` | `Band` | Optical band enum (see below). |
| `grid_ghz` | `double` | Grid spacing in GHz (e.g., `50.0` or `100.0`). |

### Band (Enum)

| Value | Name | Description |
|-------|------|-------------|
| `0` | `BAND_UNSPECIFIED` | Not specified. |
| `1` | `BAND_C_BAND` | C-band (1530--1565 nm), conventional DWDM. |
| `2` | `BAND_L_BAND` | L-band (1565--1625 nm), long wavelength. |
| `3` | `BAND_S_BAND` | S-band (1460--1530 nm), short wavelength. |

### GeoCoordinates

| Field | Type | Description |
|-------|------|-------------|
| `lat` | `double` | Latitude in decimal degrees (-90 to 90). |
| `long` | `double` | Longitude in decimal degrees (-180 to 180). |

### Endpoint

A physical or logical termination point in the optical network.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique endpoint identifier. |
| `node_id` | `string` | Parent network node identifier. |
| `operator_id` | `string` | Owning operator identifier. |
| `address` | `string` | Network address (IP:port or DNS). |
| `awg_port` | `int32` | Arrayed Waveguide Grating port number. |
| `coordinates` | `GeoCoordinates` | Geographic location. |
| `status` | `EndpointStatus` | Current endpoint status. |
| `created_at` | `Timestamp` | Creation timestamp (RFC 3339). |

### EndpointStatus (Enum)

| Value | Name | Description |
|-------|------|-------------|
| `0` | `ENDPOINT_UNSPECIFIED` | Not specified. |
| `1` | `ENDPOINT_ACTIVE` | Endpoint is active and accepting leases. |
| `2` | `ENDPOINT_INACTIVE` | Endpoint is inactive. |
| `3` | `ENDPOINT_SUSPENDED` | Endpoint is suspended by federation. |

### Lease

A wavelength lease allocation on a specific endpoint.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique lease identifier. |
| `wavelength` | `Wavelength` | Leased wavelength specification. |
| `endpoint` | `Endpoint` | Endpoint where the lease is active. |
| `operator_id` | `string` | Lease owner operator ID. |
| `status` | `LeaseStatus` | Current lease status. |
| `start_time` | `Timestamp` | Lease start time (RFC 3339). |
| `end_time` | `Timestamp` | Lease expiration time (RFC 3339). |
| `created_at` | `Timestamp` | Creation timestamp. |
| `updated_at` | `Timestamp` | Last modification timestamp. |
| `token_hash` | `bytes` (base64) | SHA-256 hash of the signed lease token. |
| `parent_hash` | `bytes` (base64) | Hash of the previous lease in the operator's chain. |

### LeaseStatus (Enum)

| Value | Name | Description |
|-------|------|-------------|
| `0` | `LEASE_UNSPECIFIED` | Not specified. |
| `1` | `LEASE_ACTIVE` | Lease is currently active. |
| `2` | `LEASE_EXPIRED` | Lease has expired. |
| `3` | `LEASE_REVOKED` | Lease was revoked before expiry. |
| `4` | `LEASE_PENDING` | Lease is awaiting confirmation. |

### LeaseToken

Cryptographically signed token proving lease validity.

| Field | Type | Description |
|-------|------|-------------|
| `version` | `int32` | Token format version (currently `1`). |
| `lease_id` | `string` | Referenced lease identifier. |
| `operator_id` | `string` | Issuing operator identifier. |
| `wavelength` | `Wavelength` | Wavelength covered by the token. |
| `endpoint_id` | `string` | Endpoint covered by the token. |
| `start_time` | `Timestamp` | Token validity start. |
| `end_time` | `Timestamp` | Token validity end. |
| `nonce` | `bytes` (base64) | Cryptographic nonce for replay protection. |
| `signature` | `bytes` (base64) | ECDSA P-256 signature over all other fields. |
| `issued_at` | `Timestamp` | Token issuance timestamp. |

### MerkleCommitment

Signed root hash of an operator's Merkle tree at a specific block height.

| Field | Type | Description |
|-------|------|-------------|
| `operator_id` | `string` | Operator identifier. |
| `root_hash` | `bytes` (base64) | Merkle tree root hash (SHA-256). |
| `timestamp` | `Timestamp` | Commitment timestamp. |
| `signature` | `bytes` (base64) | Operator's ECDSA signature over the root hash. |
| `lease_count` | `int32` | Number of leases included in the tree. |
| `block_height` | `int64` | Blockchain block height for this commitment. |

### Operator

A participant in the federated registry.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique operator identifier. |
| `name` | `string` | Human-readable operator name. |
| `public_key` | `bytes` (base64) | Operator's ECDSA P-256 public key (PKIX DER). |
| `endpoint` | `string` | gRPC endpoint address for this operator. |
| `status` | `OperatorStatus` | Current operator status. |
| `joined_at` | `Timestamp` | Registration timestamp. |
| `last_seen` | `Timestamp` | Last heartbeat timestamp. |

### OperatorStatus (Enum)

| Value | Name | Description |
|-------|------|-------------|
| `0` | `OPERATOR_UNSPECIFIED` | Not specified. |
| `1` | `OPERATOR_ACTIVE` | Operator is active in the federation. |
| `2` | `OPERATOR_INACTIVE` | Operator is inactive (no recent heartbeats). |
| `3` | `OPERATOR_SUSPENDED` | Operator suspended due to violations. |

### TranslationEntry

A wavelength translation mapping between two operators.

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique translation identifier. |
| `from_operator` | `string` | Source operator ID. |
| `to_operator` | `string` | Destination operator ID. |
| `from_wavelength` | `Wavelength` | Source wavelength. |
| `to_wavelength` | `Wavelength` | Destination wavelength. |
| `from_awg_port` | `int32` | Source AWG port. |
| `to_awg_port` | `int32` | Destination AWG port. |
| `status` | `TranslationStatus` | Current translation status. |
| `effective_time` | `Timestamp` | When the translation becomes effective. |
| `expiry_time` | `Timestamp` | When the translation expires. |

### TranslationStatus (Enum)

| Value | Name | Description |
|-------|------|-------------|
| `0` | `TRANSLATION_UNSPECIFIED` | Not specified. |
| `1` | `TRANSLATION_ACTIVE` | Translation is active and operational. |
| `2` | `TRANSLATION_PENDING` | Translation is pending setup. |
| `3` | `TRANSLATION_EXPIRED` | Translation has expired. |

### RegistryUpdate

A streaming event describing a registry mutation.

| Field | Type | Description |
|-------|------|-------------|
| `operation` | `string` | Operation type: `LEASE_CREATED`, `LEASE_RENEWED`, `LEASE_REVOKED`, `MERKLE_COMMITMENT`, `OPERATOR_REGISTERED`, `TRANSLATION_CREATED`. |
| `lease` | `Lease` | Affected lease, if applicable. |
| `commitment` | `MerkleCommitment` | New commitment, if applicable. |
| `block_height` | `int64` | Block height at which the event occurred. |
| `timestamp` | `Timestamp` | Event timestamp. |

### VerificationResult

Result of lease token verification.

| Field | Type | Description |
|-------|------|-------------|
| `valid` | `bool` | Whether the token is cryptographically valid. |
| `reason` | `string` | Human-readable explanation. |
| `computed_hash` | `bytes` (base64) | Hash computed from the token. |
| `stored_hash` | `bytes` (base64) | Hash stored in the registry. |

### InvalidityResult

Result of a proof-of-invalidity submission.

| Field | Type | Description |
|-------|------|-------------|
| `accepted` | `bool` | Whether the proof was accepted by the federation. |
| `resolution` | `string` | Human-readable resolution description. |

---

## 8. Streaming API

The `StreamRegistryUpdates` endpoint provides real-time notifications for all registry mutations. This is the recommended mechanism for keeping local state synchronized with the federation.

### gRPC Streaming

```go
stream, err := client.StreamRegistryUpdates(ctx, &flrv1.StreamRegistryUpdatesRequest{
    OperatorId:       "operator-alpha",
    FromBlockHeight:  18446744073700,
})
for {
    update, err := stream.Recv()
    if err == io.EOF {
        break
    }
    if err != nil {
        log.Fatalf("stream error: %v", err)
    }
    fmt.Printf("op=%s block=%d\n", update.Operation, update.BlockHeight)
}
```

### Server-Sent Events (REST)

The REST endpoint uses the SSE protocol. Connect with any HTTP client that supports streaming:

```bash
curl -N --cacert ca-chain.pem \
     --cert operator.crt \
     --key operator.key \
     -H "Accept: text/event-stream" \
     "https://api.flr.otap.network:8080/v1/stream?operator_id=operator-alpha"
```

#### Event Format

Each SSE event has the following structure:

```
event: registry_update
id: 18446744073709
retry: 5000
data: {"operation":"LEASE_CREATED","lease":{"id":"lease-42",...},"block_height":18446744073709,"timestamp":"2025-06-24T10:00:00Z"}

```

#### Event Types

| Event Name | Description |
|------------|-------------|
| `registry_update` | A registry mutation occurred (see `RegistryUpdate` data type). |
| `heartbeat` | Keep-alive sent every 30 seconds when no mutations occur. |
| `error` | An error occurred; the connection may close. |

#### Reconnection

The SSE endpoint sends a `retry: 5000` hint. Clients should automatically reconnect with exponential backoff on disconnect. The `Last-Event-ID` header can be used to resume from the last received block height:

```bash
curl -N -H "Accept: text/event-stream" \
     -H "Last-Event-ID: 18446744073709" \
     https://api.flr.otap.network:8080/v1/stream
```

---

## 9. Error Format

All errors, whether over gRPC or REST, follow a consistent structure.

### gRPC Errors

```go
st, _ := status.FromError(err)
fmt.Printf("Code: %s\n", st.Code())          // NOT_FOUND
fmt.Printf("Message: %s\n", st.Message())    // lease not found
```

### REST Errors

```json
{
  "code": "NOT_FOUND",
  "message": "lease 'lease-unknown' not found in registry",
  "details": {
    "resource_type": "Lease",
    "resource_id": "lease-unknown"
  }
}
```

### Error Detail Keys

Common detail keys used across endpoints:

| Key | Description |
|-----|-------------|
| `resource_type` | Type of resource (Lease, Operator, Translation, Commitment). |
| `resource_id` | Identifier of the missing/conflicting resource. |
| `field` | Field that caused the validation error. |
| `retry_after_seconds` | Seconds to wait before retrying (rate limit only). |

---

## 10. Pagination

List endpoints (`ListLeases`, `ListOperators`, `ListTranslations`) return all results matching the filter criteria. For large result sets, the `total` field indicates the total count.

### Query Parameters

All list endpoints support the following optional query parameters:

| Parameter | Type | Description |
|-----------|------|-------------|
| `page_size` | `int32` | Maximum items per page (default: 50, max: 500). |
| `page_token` | `string` | Opaque pagination token from previous response. |

### Response Headers (REST)

| Header | Description |
|--------|-------------|
| `X-Total-Count` | Total number of results matching the query. |
| `X-Next-Page-Token` | Token to fetch the next page; absent if last page. |

### Example Paginated Request

```
GET /v1/leases?status=LEASE_ACTIVE&page_size=10&page_token=CgZzZWNvbmQK
```

### Example Paginated Response

```json
{
  "leases": [ /* 10 items */ ],
  "total": 147
}
```

With headers:

```
X-Total-Count: 147
X-Next-Page-Token: CgZ0aGlyZAo=
```

---

## 11. SDK Example (Go)

The following complete example demonstrates connecting to the FLR registry using the Go gRPC SDK, creating a lease, and verifying it.

```go
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"time"

	flrv1 "github.com/otap/flr/proto/flr/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	// -----------------------------------------------------------------
	// 1. Load mTLS credentials
	// -----------------------------------------------------------------
	cert, err := tls.LoadX509KeyPair("operator.crt", "operator.key")
	if err != nil {
		log.Fatalf("failed to load client cert: %v", err)
	}

	caCert, err := os.ReadFile("ca-chain.pem")
	if err != nil {
		log.Fatalf("failed to load CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caPool,
		InsecureSkipVerify: false,
	}

	// -----------------------------------------------------------------
	// 2. Dial the gRPC endpoint
	// -----------------------------------------------------------------
	conn, err := grpc.Dial("grpc.flr.otap.network:9090",
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
	)
	if err != nil {
		log.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	client := flrv1.NewFederatedRegistryClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// -----------------------------------------------------------------
	// 3. Create a lease
	// -----------------------------------------------------------------
	createResp, err := client.CreateLease(ctx, &flrv1.CreateLeaseRequest{
		Wavelength: &flrv1.Wavelength{
			LambdaNm:   1550.12,
			ChannelNum: 42,
			Band:       flrv1.Band_BAND_C_BAND,
			GridGhz:    50.0,
		},
		EndpointId: "endpoint-nyc-01",
		OperatorId: "operator-alpha",
		StartTime:  timestamppb.New(time.Now().Add(1 * time.Hour)),
		Duration:   durationpb.New(2 * time.Hour),
	})
	if err != nil {
		log.Fatalf("CreateLease failed: %v", err)
	}
	fmt.Printf("Lease created: %s (status=%s)\n",
		createResp.Id, createResp.Status.String())

	// -----------------------------------------------------------------
	// 4. Verify the lease token
	// -----------------------------------------------------------------
	verifyResp, err := client.VerifyLease(ctx, &flrv1.VerifyLeaseRequest{
		Token: &flrv1.LeaseToken{
			Version:    1,
			LeaseId:    createResp.Id,
			OperatorId: "operator-alpha",
			Wavelength: createResp.Wavelength,
			EndpointId: "endpoint-nyc-01",
			StartTime:  createResp.StartTime,
			EndTime:    createResp.EndTime,
			Nonce:      []byte("random_nonce_123"),
			Signature:  make([]byte, 72), // placeholder
			IssuedAt:   timestamppb.Now(),
		},
		OperatorId: "operator-alpha",
	})
	if err != nil {
		log.Fatalf("VerifyLease failed: %v", err)
	}
	fmt.Printf("Token valid: %v (%s)\n", verifyResp.Valid, verifyResp.Reason)

	// -----------------------------------------------------------------
	// 5. List active leases
	// -----------------------------------------------------------------
	listResp, err := client.ListLeases(ctx, &flrv1.ListLeasesRequest{
		Status: flrv1.LeaseStatus_LEASE_ACTIVE,
	})
	if err != nil {
		log.Fatalf("ListLeases failed: %v", err)
	}
	fmt.Printf("Active leases: %d total\n", listResp.Total)
	for _, lease := range listResp.Leases {
		fmt.Printf("  - %s @ %.2fnm (ch%d)\n",
			lease.Id,
			lease.Wavelength.LambdaNm,
			lease.Wavelength.ChannelNum)
	}

	// -----------------------------------------------------------------
	// 6. Subscribe to registry updates
	// -----------------------------------------------------------------
	stream, err := client.StreamRegistryUpdates(ctx, &flrv1.StreamRegistryUpdatesRequest{
		OperatorId:      "operator-alpha",
		FromBlockHeight: 0,
	})
	if err != nil {
		log.Fatalf("StreamRegistryUpdates failed: %v", err)
	}

	go func() {
		for {
			update, err := stream.Recv()
			if err != nil {
				log.Printf("stream closed: %v", err)
				return
			}
			fmt.Printf("[stream] op=%s block=%d\n",
				update.Operation, update.BlockHeight)
		}
	}()

	time.Sleep(30 * time.Second) // collect events
}
```

### Generating Go Stubs

```bash
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/flr/v1/registry.proto
```

---

## 12. curl Examples

### Health Check

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     https://api.flr.otap.network:8080/v1/health
```

### Create a Lease

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -X POST https://api.flr.otap.network:8080/v1/leases \
     -H "Content-Type: application/json" \
     -d '{
       "wavelength": {
         "lambda_nm": 1550.12,
         "channel_num": 42,
         "band": "BAND_C_BAND",
         "grid_ghz": 50.0
       },
       "endpoint_id": "endpoint-nyc-01",
       "operator_id": "operator-alpha",
       "start_time": "2025-06-24T10:00:00Z",
       "duration": "3600s"
     }'
```

### Get a Lease

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     https://api.flr.otap.network:8080/v1/leases/lease-42-c-band-20250624
```

### Renew a Lease

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -X POST https://api.flr.otap.network:8080/v1/leases/lease-42-c-band-20250624/renew \
     -H "Content-Type: application/json" \
     -d '{"extension": "1800s"}'
```

### Revoke a Lease

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -X POST https://api.flr.otap.network:8080/v1/leases/lease-42-c-band-20250624/revoke \
     -H "Content-Type: application/json" \
     -d '{"reason": "Emergency fiber maintenance"}'
```

### List Active Leases

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     "https://api.flr.otap.network:8080/v1/leases?status=LEASE_ACTIVE"
```

### Get Merkle Commitment

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     https://api.flr.otap.network:8080/v1/commitments/operator-alpha/18446744073709
```

### Verify a Lease Token

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -X POST https://api.flr.otap.network:8080/v1/verify \
     -H "Content-Type: application/json" \
     -d '{
       "token": {
         "version": 1,
         "lease_id": "lease-42-c-band-20250624",
         "operator_id": "operator-alpha",
         "wavelength": {"lambda_nm": 1550.12, "channel_num": 42, "band": "BAND_C_BAND", "grid_ghz": 50.0},
         "endpoint_id": "endpoint-nyc-01",
         "start_time": "2025-06-24T10:00:00Z",
         "end_time": "2025-06-24T11:00:00Z",
         "nonce": "cmFuZG9tX25vbmNl",
         "signature": "3045022100ef1b...",
         "issued_at": "2025-06-24T09:59:30Z"
       },
       "operator_id": "operator-alpha"
     }'
```

### Submit Proof of Invalidity

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -X POST https://api.flr.otap.network:8080/v1/invalidity \
     -H "Content-Type: application/json" \
     -d '{
       "type": "DOUBLE_ALLOCATION",
       "lease_a_id": "lease-42-c-band-20250624",
       "lease_b_id": "lease-42-c-band-20250624-dup",
       "operator_id": "operator-beta",
       "merkle_proof": ["aGVhcjE...", "dGhlcmUy..."]
     }'
```

### Register an Operator

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -X POST https://api.flr.otap.network:8080/v1/operators \
     -H "Content-Type: application/json" \
     -d '{
       "id": "operator-gamma",
       "name": "Gamma Optical Networks",
       "public_key": "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE...",
       "endpoint": "grpc.operator-gamma.example.com:9090"
     }'
```

### Get an Operator

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     https://api.flr.otap.network:8080/v1/operators/operator-alpha
```

### List Active Operators

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     "https://api.flr.otap.network:8080/v1/operators?status=OPERATOR_ACTIVE"
```

### Create a Translation

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -X POST https://api.flr.otap.network:8080/v1/translations \
     -H "Content-Type: application/json" \
     -d '{
       "from_operator": "operator-alpha",
       "to_operator": "operator-beta",
       "from_wavelength": {"lambda_nm": 1550.12, "channel_num": 42, "band": "BAND_C_BAND", "grid_ghz": 50.0},
       "to_wavelength": {"lambda_nm": 1551.72, "channel_num": 44, "band": "BAND_C_BAND", "grid_ghz": 50.0},
       "from_awg_port": 7,
       "to_awg_port": 12,
       "duration": "7200s"
     }'
```

### Get a Translation

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     https://api.flr.otap.network:8080/v1/translations/trans-alpha-beta-20250624
```

### List Translations

```bash
curl --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     "https://api.flr.otap.network:8080/v1/translations?status=TRANSLATION_ACTIVE"
```

### Subscribe to SSE Stream

```bash
curl -N --cacert ca-chain.pem \
     --cert operator.crt --key operator.key \
     -H "Accept: text/event-stream" \
     "https://api.flr.otap.network:8080/v1/stream?operator_id=operator-alpha"
```

---

## Appendix A: Proto File Location

The canonical Protocol Buffers definition is maintained at:

```
github.com/otap/flr/proto/flr/v1/registry.proto
```

Generate client stubs using `protoc` with the appropriate language plugins. The Go package path is:

```
github.com/otap/flr/proto/flr/v1;flrv1
```

## Appendix B: Changelog

| Version | Date | Changes |
|---------|------|---------|
| 1.0.0 | 2025-06-24 | Initial API release with 15 endpoints. |

---

*Copyright (c) 2025 OTAP Network Federation. All rights reserved.*
