//! Three-way handshake state machine for D3 session establishment.
//!
//! Uses typed state transitions to prevent protocol misuse at compile time:
//!   Initiator → InitiatorWaitMsg2 → (D3Session, HsMsg3)
//!   Responder → ResponderWaitMsg3 → D3Session
//!
//! Wire format: all messages are fixed-size byte arrays for simple
//! `read_exact` / `write_all` over any transport (TCP, QUIC, MODQ control channel).

use rand::RngCore;
use sha2::{Digest, Sha256};

use crate::key_domain::{D3SigningKey, D3VerifyingKey, NONCE_LEN, PUBKEY_LEN, SIG_LEN};

/// Errors during handshake.
#[derive(Debug, thiserror::Error)]
pub enum HsError {
    #[error("malformed message: expected {expected} bytes, got {got}")]
    Malformed { expected: usize, got: usize },

    #[error("bad server signature in Msg2")]
    BadServerSig,

    #[error("bad client signature in Msg3")]
    BadClientSig,

    #[error("client not authorized")]
    NotAuthorized,

    #[error("transport error: {0}")]
    Transport(String),
}

/// ACL trait — the responder checks whether a client public key is permitted.
pub trait ClientAcl {
    fn is_authorized(&self, client_pk: &[u8; PUBKEY_LEN]) -> bool;
}

/// Allow-all ACL for testing.
pub struct AllowAll;
impl ClientAcl for AllowAll {
    fn is_authorized(&self, _: &[u8; PUBKEY_LEN]) -> bool {
        true
    }
}

// ============================================================================
// Wire-format messages
// ============================================================================

/// Msg1: Initiator → Responder. Contains initiator pubkey + nonce.
pub struct HsMsg1 {
    pub client_pk: [u8; PUBKEY_LEN],
    pub nonce_i: [u8; NONCE_LEN],
}

/// Msg2: Responder → Initiator. Contains responder pubkey + nonce + signature.
pub struct HsMsg2 {
    pub server_pk: [u8; PUBKEY_LEN],
    pub nonce_r: [u8; NONCE_LEN],
    pub sig_r: [u8; SIG_LEN],
}

/// Msg3: Initiator → Responder. Contains initiator pubkey + signature.
pub struct HsMsg3 {
    pub client_pk: [u8; PUBKEY_LEN],
    pub sig_i: [u8; SIG_LEN],
}

/// Size constants for read_exact / write_all.
pub const MSG1_LEN: usize = PUBKEY_LEN + NONCE_LEN;
pub const MSG2_LEN: usize = PUBKEY_LEN + NONCE_LEN + SIG_LEN;
pub const MSG3_LEN: usize = PUBKEY_LEN + SIG_LEN;

impl HsMsg1 {
    pub fn encode(&self) -> [u8; MSG1_LEN] {
        let mut buf = [0u8; MSG1_LEN];
        buf[..PUBKEY_LEN].copy_from_slice(&self.client_pk);
        buf[PUBKEY_LEN..].copy_from_slice(&self.nonce_i);
        buf
    }

    pub fn decode(buf: &[u8; MSG1_LEN]) -> Result<Self, HsError> {
        let mut client_pk = [0u8; PUBKEY_LEN];
        let mut nonce_i = [0u8; NONCE_LEN];
        client_pk.copy_from_slice(&buf[..PUBKEY_LEN]);
        nonce_i.copy_from_slice(&buf[PUBKEY_LEN..]);
        Ok(Self { client_pk, nonce_i })
    }
}

impl HsMsg2 {
    pub fn encode(&self) -> [u8; MSG2_LEN] {
        let mut buf = [0u8; MSG2_LEN];
        buf[..PUBKEY_LEN].copy_from_slice(&self.server_pk);
        buf[PUBKEY_LEN..PUBKEY_LEN + NONCE_LEN].copy_from_slice(&self.nonce_r);
        buf[PUBKEY_LEN + NONCE_LEN..].copy_from_slice(&self.sig_r);
        buf
    }

    pub fn decode(buf: &[u8; MSG2_LEN]) -> Result<Self, HsError> {
        let mut server_pk = [0u8; PUBKEY_LEN];
        let mut nonce_r = [0u8; NONCE_LEN];
        let mut sig_r = [0u8; SIG_LEN];
        server_pk.copy_from_slice(&buf[..PUBKEY_LEN]);
        nonce_r.copy_from_slice(&buf[PUBKEY_LEN..PUBKEY_LEN + NONCE_LEN]);
        sig_r.copy_from_slice(&buf[PUBKEY_LEN + NONCE_LEN..]);
        Ok(Self {
            server_pk,
            nonce_r,
            sig_r,
        })
    }
}

impl HsMsg3 {
    pub fn encode(&self) -> [u8; MSG3_LEN] {
        let mut buf = [0u8; MSG3_LEN];
        buf[..PUBKEY_LEN].copy_from_slice(&self.client_pk);
        buf[PUBKEY_LEN..].copy_from_slice(&self.sig_i);
        buf
    }

    pub fn decode(buf: &[u8; MSG3_LEN]) -> Result<Self, HsError> {
        let mut client_pk = [0u8; PUBKEY_LEN];
        let mut sig_i = [0u8; SIG_LEN];
        client_pk.copy_from_slice(&buf[..PUBKEY_LEN]);
        sig_i.copy_from_slice(&buf[PUBKEY_LEN..]);
        Ok(Self { client_pk, sig_i })
    }
}

// ============================================================================
// Handshake helper: hash nonces with a domain prefix
// ============================================================================

fn hash_nonces(prefix: &[u8], n1: &[u8; NONCE_LEN], n2: &[u8; NONCE_LEN]) -> Vec<u8> {
    let mut h = Sha256::new();
    h.update(prefix);
    h.update(n1);
    h.update(n2);
    h.finalize().to_vec()
}

/// Derive the epoch nonce from the two handshake nonces.
pub fn derive_epoch_nonce(n_i: &[u8; NONCE_LEN], n_r: &[u8; NONCE_LEN]) -> u64 {
    let digest = hash_nonces(b"OTAP-EPOCH-v1", n_i, n_r);
    let mut bytes = [0u8; 8];
    bytes.copy_from_slice(&digest[..8]);
    u64::from_be_bytes(bytes)
}

// ============================================================================
// Typed state machine: Initiator side
// ============================================================================

/// Initiator start state. Consumed by `start()`.
pub struct Initiator {
    sk: D3SigningKey,
    peer_pk: D3VerifyingKey,
}

/// Initiator waiting for Msg2. Consumed by `finish()`.
pub struct InitiatorWaitMsg2 {
    sk: D3SigningKey,
    peer_pk: D3VerifyingKey,
    nonce_i: [u8; NONCE_LEN],
}

/// Result of a completed initiator handshake.
pub struct EstablishedInitiator {
    pub epoch_nonce: u64,
    pub peer_pk: D3VerifyingKey,
}

impl Initiator {
    pub fn new(sk: D3SigningKey, peer_pk: D3VerifyingKey) -> Self {
        Self { sk, peer_pk }
    }

    /// Generate Msg1 and transition to WaitMsg2.
    pub fn start(self) -> (InitiatorWaitMsg2, HsMsg1) {
        let mut nonce_i = [0u8; NONCE_LEN];
        rand::rngs::OsRng.fill_bytes(&mut nonce_i);

        let msg1 = HsMsg1 {
            client_pk: self.sk.verifying_key().as_bytes(),
            nonce_i,
        };

        let wait = InitiatorWaitMsg2 {
            sk: self.sk,
            peer_pk: self.peer_pk,
            nonce_i,
        };

        (wait, msg1)
    }
}

impl InitiatorWaitMsg2 {
    /// Process Msg2, verify server signature, generate Msg3.
    pub fn finish(self, msg2: HsMsg2) -> Result<(EstablishedInitiator, HsMsg3), HsError> {
        // Verify server signed hash("HELLO-R" || nonce_i || nonce_r)
        let digest = hash_nonces(b"HELLO-R", &self.nonce_i, &msg2.nonce_r);
        self.peer_pk
            .verify_raw(&digest, &msg2.sig_r)
            .map_err(|_| HsError::BadServerSig)?;

        // Sign hash("HELLO-I" || nonce_r || nonce_i)
        let our_digest = hash_nonces(b"HELLO-I", &msg2.nonce_r, &self.nonce_i);
        let sig_i = self.sk.sign_raw(&our_digest);

        let epoch_nonce = derive_epoch_nonce(&self.nonce_i, &msg2.nonce_r);

        let established = EstablishedInitiator {
            epoch_nonce,
            peer_pk: self.peer_pk,
        };

        let msg3 = HsMsg3 {
            client_pk: self.sk.verifying_key().as_bytes(),
            sig_i,
        };

        Ok((established, msg3))
    }
}

// ============================================================================
// Typed state machine: Responder side
// ============================================================================

/// Responder start state.
pub struct Responder<'a, A: ClientAcl> {
    sk: D3SigningKey,
    acl: &'a A,
}

/// Responder waiting for Msg3.
pub struct ResponderWaitMsg3 {
    sk: D3SigningKey,
    client_pk: D3VerifyingKey,
    nonce_i: [u8; NONCE_LEN],
    nonce_r: [u8; NONCE_LEN],
}

/// Result of a completed responder handshake.
pub struct EstablishedResponder {
    pub epoch_nonce: u64,
    pub peer_pk: D3VerifyingKey,
}

impl<'a, A: ClientAcl> Responder<'a, A> {
    pub fn new(sk: D3SigningKey, acl: &'a A) -> Self {
        Self { sk, acl }
    }

    /// Process Msg1, check ACL, generate Msg2.
    pub fn handle_msg1(self, msg1: HsMsg1) -> Result<(ResponderWaitMsg3, HsMsg2), HsError> {
        // ACL check
        if !self.acl.is_authorized(&msg1.client_pk) {
            return Err(HsError::NotAuthorized);
        }

        let client_pk =
            D3VerifyingKey::from_bytes(&msg1.client_pk).map_err(|_| HsError::BadClientSig)?;

        let mut nonce_r = [0u8; NONCE_LEN];
        rand::rngs::OsRng.fill_bytes(&mut nonce_r);

        // Sign hash("HELLO-R" || nonce_i || nonce_r)
        let digest = hash_nonces(b"HELLO-R", &msg1.nonce_i, &nonce_r);
        let sig_r = self.sk.sign_raw(&digest);

        let msg2 = HsMsg2 {
            server_pk: self.sk.verifying_key().as_bytes(),
            nonce_r,
            sig_r,
        };

        let wait = ResponderWaitMsg3 {
            sk: self.sk,
            client_pk,
            nonce_i: msg1.nonce_i,
            nonce_r,
        };

        Ok((wait, msg2))
    }
}

impl ResponderWaitMsg3 {
    /// Process Msg3, verify client signature, establish session.
    pub fn handle_msg3(self, msg3: HsMsg3) -> Result<EstablishedResponder, HsError> {
        // Verify client signed hash("HELLO-I" || nonce_r || nonce_i)
        let digest = hash_nonces(b"HELLO-I", &self.nonce_r, &self.nonce_i);
        self.client_pk
            .verify_raw(&digest, &msg3.sig_i)
            .map_err(|_| HsError::BadClientSig)?;

        let epoch_nonce = derive_epoch_nonce(&self.nonce_i, &self.nonce_r);

        Ok(EstablishedResponder {
            epoch_nonce,
            peer_pk: self.client_pk,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn full_handshake_derives_same_epoch() {
        let alice_sk = D3SigningKey::from_bytes(&[1u8; 32]);
        let bob_sk = D3SigningKey::from_bytes(&[2u8; 32]);

        let alice_pk = alice_sk.verifying_key();
        let bob_pk = bob_sk.verifying_key();

        // Alice initiates
        let (alice_wait, msg1) = Initiator::new(alice_sk.clone(), bob_pk.clone()).start();

        // Bob responds
        let responder = Responder::new(bob_sk.clone(), &AllowAll);
        let (bob_wait, msg2) = responder.handle_msg1(msg1).unwrap();

        // Alice finishes
        let (alice_est, msg3) = alice_wait.finish(msg2).unwrap();

        // Bob finishes
        let bob_est = bob_wait.handle_msg3(msg3).unwrap();

        assert_eq!(alice_est.epoch_nonce, bob_est.epoch_nonce);
        assert_ne!(alice_est.epoch_nonce, 0);
    }

    #[test]
    fn bad_server_key_rejects() {
        let alice_sk = D3SigningKey::from_bytes(&[1u8; 32]);
        let bob_sk = D3SigningKey::from_bytes(&[2u8; 32]);
        let eve_sk = D3SigningKey::from_bytes(&[99u8; 32]);

        // Alice expects Bob but Eve responds
        let (alice_wait, msg1) = Initiator::new(alice_sk, bob_sk.verifying_key()).start();

        let eve_resp = Responder::new(eve_sk, &AllowAll);
        let (_, msg2) = eve_resp.handle_msg1(msg1).unwrap();

        // Alice rejects — Eve's signature doesn't match Bob's pubkey
        assert!(alice_wait.finish(msg2).is_err());
    }

    #[test]
    fn wire_format_roundtrip() {
        let msg1 = HsMsg1 {
            client_pk: [0xAA; PUBKEY_LEN],
            nonce_i: [0xBB; NONCE_LEN],
        };
        let encoded = msg1.encode();
        let decoded = HsMsg1::decode(&encoded).unwrap();
        assert_eq!(decoded.client_pk, msg1.client_pk);
        assert_eq!(decoded.nonce_i, msg1.nonce_i);

        let msg2 = HsMsg2 {
            server_pk: [0xCC; PUBKEY_LEN],
            nonce_r: [0xDD; NONCE_LEN],
            sig_r: [0xEE; SIG_LEN],
        };
        let encoded = msg2.encode();
        let decoded = HsMsg2::decode(&encoded).unwrap();
        assert_eq!(decoded.server_pk, msg2.server_pk);
        assert_eq!(decoded.nonce_r, msg2.nonce_r);
        assert_eq!(decoded.sig_r, msg2.sig_r);
    }
}
