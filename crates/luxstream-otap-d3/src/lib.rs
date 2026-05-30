//! # LuxStream OTAP/D3 — Session Establishment & Frame Authentication
//!
//! Complete session protocol stack:
//!
//! 1. **`key_domain`** — Type-level separation of D3 (Ed25519) and FLR (P-256) keys.
//!    The compiler prevents cross-domain key misuse.
//!
//! 2. **`rpc`** — Rolling Photon Checksum with domain tag and epoch nonce mixing.
//!    Integrity check, not a security boundary.
//!
//! 3. **`handshake`** — Three-way typed state machine (Initiator/Responder) with
//!    wire-format encode/decode. Derives epoch_nonce from exchange nonces.
//!
//! 4. **`session`** — `D3Session` wrapping seal/open operations. Constructed from
//!    a completed handshake via `from_client_handshake` / `from_server_handshake`.
//!    D3 wraps RPC. Always. Inversion creates a forgery oracle.
//!
//! 5. **`transport`** — Tokio async transport driving the handshake over TCP sockets.
//!
//! 6. **`replay_cache`** — Disk-backed ring buffer of transcript hashes with
//!    constant-time comparison. Survives daemon restarts.
//!
//! ## Data Flow
//!
//! ```text
//! Handshake:  Initiator ──Msg1──► Responder
//!             Initiator ◄──Msg2── Responder
//!             Initiator ──Msg3──► Responder
//!                    ↓                ↓
//!               D3Session        D3Session
//!               (same epoch_nonce on both sides)
//!
//! Frame Auth: seal(domain, symbols)
//!               → compute RPC(symbols, domain, epoch_nonce)
//!               → SHA-256(domain_tag || epoch || symbols || rpc)
//!               → Ed25519 sign
//!               → AuthenticatedFrame { symbols, rpc_tag, d3_sig }
//!
//!             open(domain, frame)
//!               → Ed25519 verify (step 1: crypto boundary)
//!               → recompute RPC (step 2: defense-in-depth)
//!               → return &[Symbol]
//! ```

pub mod handshake;
pub mod key_domain;
pub mod replay_cache;
pub mod rpc;
pub mod session;

#[cfg(feature = "transport")]
pub mod transport;

// Re-export the main entry points for ergonomic use.
pub use handshake::{HsMsg1, HsMsg2, HsMsg3, Initiator, Responder};
pub use key_domain::{D3SigningKey, D3VerifyingKey, FlrSigningKey, FlrVerifyingKey};
pub use replay_cache::PersistentReplayCache;
pub use rpc::{calculate_rpc, Polarization, RpcDomain, Symbol};
pub use session::{AuthenticatedFrame, D3Session};
