//! D3 Session — authenticated frame seal/open using domain-typed keys.
//!
//! Constructed from a completed handshake via `from_client_handshake` or
//! `from_server_handshake`. These adapters ensure the session holds
//! `D3SigningKey` / `D3VerifyingKey`, not raw dalek types.

use sha2::{Digest, Sha256};

use crate::handshake::{EstablishedInitiator, EstablishedResponder};
use crate::key_domain::{D3SigningKey, D3VerifyingKey, SIG_LEN};
use crate::rpc::{calculate_rpc, RpcDomain, Symbol};

/// A live D3 session with a shared epoch nonce and per-side keys.
pub struct D3Session {
    /// Epoch nonce derived from the handshake. Both sides agree on this value.
    pub epoch_nonce: u64,
    /// Our signing key (D3 domain only).
    signing_key: D3SigningKey,
    /// Peer's verifying key (D3 domain only).
    peer_key: D3VerifyingKey,
}

/// An authenticated frame ready for transmission or post-verification.
#[derive(Debug, Clone)]
pub struct AuthenticatedFrame {
    pub symbols: Vec<Symbol>,
    pub rpc_tag: u32,
    pub d3_sig: [u8; SIG_LEN],
}

impl D3Session {
    /// Construct a session from a completed initiator handshake.
    pub fn from_client_handshake(established: EstablishedInitiator, our_sk: D3SigningKey) -> Self {
        Self {
            epoch_nonce: established.epoch_nonce,
            signing_key: our_sk,
            peer_key: established.peer_pk,
        }
    }

    /// Construct a session from a completed responder handshake.
    pub fn from_server_handshake(established: EstablishedResponder, our_sk: D3SigningKey) -> Self {
        Self {
            epoch_nonce: established.epoch_nonce,
            signing_key: our_sk,
            peer_key: established.peer_pk,
        }
    }

    /// Seal a frame: compute RPC, build canonical preimage, sign with Ed25519.
    pub fn seal(&self, domain: RpcDomain, symbols: Vec<Symbol>) -> AuthenticatedFrame {
        let rpc_tag = calculate_rpc(&symbols, domain as u32, self.epoch_nonce);
        let preimage = canonical_preimage(domain, self.epoch_nonce, &symbols, rpc_tag);
        let sig = self.signing_key.sign_raw(&preimage);

        AuthenticatedFrame {
            symbols,
            rpc_tag,
            d3_sig: sig,
        }
    }

    /// Open a frame: verify Ed25519 signature, recompute RPC, reject if either fails.
    ///
    /// Order of operations (per OTAP_D3_INTEGRATION.md):
    /// 1. Verify D3 signature (constant-time).
    /// 2. Recompute RPC (defense-in-depth).
    /// 3. Return symbols upstack.
    pub fn open<'a>(
        &self,
        domain: RpcDomain,
        frame: &'a AuthenticatedFrame,
    ) -> Result<&'a [Symbol], &'static str> {
        // Step 1: D3 signature verification
        let preimage = canonical_preimage(domain, self.epoch_nonce, &frame.symbols, frame.rpc_tag);
        self.peer_key
            .verify_raw(&preimage, &frame.d3_sig)
            .map_err(|_| "D3: signature verification failed")?;

        // Step 2: RPC re-verification (defense-in-depth)
        let recomputed = calculate_rpc(&frame.symbols, domain as u32, self.epoch_nonce);
        if recomputed != frame.rpc_tag {
            return Err("RPC: re-verification failed — sender bug or fault injection");
        }

        Ok(&frame.symbols)
    }
}

/// Build the canonical preimage for D3 signing.
///
/// `SHA-256("LUXSTREAM-OTAP-D3-v1" || domain || epoch_nonce || len || symbols || rpc_tag)`
fn canonical_preimage(
    domain: RpcDomain,
    epoch_nonce: u64,
    symbols: &[Symbol],
    rpc_tag: u32,
) -> Vec<u8> {
    let mut h = Sha256::new();
    h.update(b"LUXSTREAM-OTAP-D3-v1");
    h.update((domain as u32).to_be_bytes());
    h.update(epoch_nonce.to_be_bytes());
    h.update((symbols.len() as u32).to_be_bytes());
    for s in symbols {
        let val_bytes = s.value.to_be_bytes();
        h.update([val_bytes[0], val_bytes[1], s.lambda, s.pol as u8]);
    }
    h.update(rpc_tag.to_be_bytes());
    h.finalize().to_vec()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::handshake::{AllowAll, Initiator, Responder};
    use crate::rpc::Polarization;

    fn make_session_pair() -> (D3Session, D3Session) {
        let alice_sk = D3SigningKey::from_bytes(&[1u8; 32]);
        let bob_sk = D3SigningKey::from_bytes(&[2u8; 32]);

        let (alice_wait, msg1) = Initiator::new(alice_sk.clone(), bob_sk.verifying_key()).start();
        let (bob_wait, msg2) = Responder::new(bob_sk.clone(), &AllowAll)
            .handle_msg1(msg1)
            .unwrap();
        let (alice_est, msg3) = alice_wait.finish(msg2).unwrap();
        let bob_est = bob_wait.handle_msg3(msg3).unwrap();

        let alice_session = D3Session::from_client_handshake(alice_est, alice_sk);
        let bob_session = D3Session::from_server_handshake(bob_est, bob_sk);

        assert_eq!(alice_session.epoch_nonce, bob_session.epoch_nonce);
        (alice_session, bob_session)
    }

    #[test]
    fn seal_open_roundtrip() {
        let (alice, bob) = make_session_pair();

        let payload = vec![
            Symbol {
                value: 1023,
                lambda: 34,
                pol: Polarization::H,
            },
            Symbol {
                value: 500,
                lambda: 34,
                pol: Polarization::V,
            },
        ];

        let frame = alice.seal(RpcDomain::LinkData, payload.clone());
        let opened = bob.open(RpcDomain::LinkData, &frame).unwrap();
        assert_eq!(opened.len(), 2);
        assert_eq!(opened[0].value, 1023);
        assert_eq!(opened[1].value, 500);
    }

    #[test]
    fn forgery_detection() {
        let (alice, bob) = make_session_pair();

        let payload = vec![Symbol {
            value: 42,
            lambda: 1,
            pol: Polarization::D,
        }];
        let mut frame = alice.seal(RpcDomain::LinkData, payload);

        // Tamper with the payload
        frame.symbols[0].value = 999;
        // Even if the attacker recomputes RPC, the D3 signature is over the
        // ORIGINAL preimage and will fail.
        frame.rpc_tag = calculate_rpc(
            &frame.symbols,
            RpcDomain::LinkData as u32,
            alice.epoch_nonce,
        );

        assert!(bob.open(RpcDomain::LinkData, &frame).is_err());
    }

    #[test]
    fn cross_domain_rejection() {
        let (alice, bob) = make_session_pair();

        let payload = vec![Symbol {
            value: 100,
            lambda: 10,
            pol: Polarization::A,
        }];
        let frame = alice.seal(RpcDomain::LinkData, payload);

        // Try to open with ControlPlane domain → different preimage → sig fails
        assert!(bob.open(RpcDomain::ControlPlane, &frame).is_err());
    }
}
