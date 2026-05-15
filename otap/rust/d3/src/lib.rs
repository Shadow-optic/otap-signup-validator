//! OTAP D3 Cryptographic Session Layer
//!
//! Provides:
//! - Ed25519-based 3-way handshake
//! - Authenticated frame sealing/opening
//! - Rolling Photon Checksum (RPC)

use ed25519_dalek::{Signature, SigningKey, VerifyingKey, Signer, Verifier};
use sha2::{Digest, Sha256};
use rand::rngs::OsRng;
use rand::RngCore;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[repr(u8)]
pub enum Polarization {
    H = 1,
    V = 2,
    D = 3,
    A = 5,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[repr(C)]
pub struct Symbol {
    pub value: u16,
    pub lambda: u8,
    pub pol: Polarization,
}

#[derive(Clone, Copy, Debug)]
pub enum RpcDomain {
    LinkData = 0,
    ControlPlane = 1,
}

/// Rolling Photon Checksum (domain-separated integrity check)
pub fn calculate_rpc(symbols: &[Symbol], domain: u32, epoch_nonce: u64) -> u32 {
    const PRIME: u64 = 7919;
    let mut acc: u64 = epoch_nonce.wrapping_add((domain as u64) * 31);

    for s in symbols {
        let w_i = (s.lambda as u64) * PRIME;
        let p_i = s.pol as u64;
        let term = (s.value as u64).wrapping_mul(w_i).wrapping_mul(p_i);
        acc = acc.wrapping_add(term);
    }

    (acc & 0xFFFFFFFF) as u32
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AuthenticatedFrame {
    pub symbols: Vec<Symbol>,
    pub rpc_tag: u32,
    #[serde(with = "serde_bytes_array")]
    pub d3_sig: [u8; 64],
}

/// Helper module so `[u8; 64]` round-trips through serde without depending on
/// const-generics support in serde itself.
mod serde_bytes_array {
    use serde::{Deserialize, Deserializer, Serializer};

    pub fn serialize<S: Serializer>(bytes: &[u8; 64], s: S) -> Result<S::Ok, S::Error> {
        serde_bytes::Bytes::new(bytes).serialize(s)
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<[u8; 64], D::Error> {
        let v: Vec<u8> = serde_bytes::ByteBuf::deserialize(d)?.into_vec();
        let arr: [u8; 64] = v
            .try_into()
            .map_err(|v: Vec<u8>| serde::de::Error::invalid_length(v.len(), &"64"))?;
        Ok(arr)
    }

    use serde::Serialize;
}

#[derive(Debug, Clone)]
pub struct D3Session {
    pub epoch_nonce: u64,
    pub signing_key: SigningKey,
    pub peer_key: VerifyingKey,
}

impl D3Session {
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
            h.update(s.value.to_be_bytes());
            h.update(&[s.lambda, s.pol as u8]);
        }

        h.update(rpc_tag.to_be_bytes());
        h.finalize().to_vec()
    }

    pub fn seal(&self, domain: RpcDomain, symbols: Vec<Symbol>) -> Result<AuthenticatedFrame, &'static str> {
        let rpc_tag = calculate_rpc(&symbols, domain as u32, self.epoch_nonce);
        let preimage = Self::canonical_preimage(domain, self.epoch_nonce, &symbols, rpc_tag);
        let sig = self.signing_key.sign(&preimage);

        Ok(AuthenticatedFrame {
            symbols,
            rpc_tag,
            d3_sig: sig.to_bytes(),
        })
    }

    pub fn open(&self, domain: RpcDomain, frame: &AuthenticatedFrame) -> Result<Vec<Symbol>, &'static str> {
        let preimage = Self::canonical_preimage(domain, self.epoch_nonce, &frame.symbols, frame.rpc_tag);
        let sig = Signature::from_bytes(&frame.d3_sig);

        self.peer_key
            .verify(&preimage, &sig)
            .map_err(|_| "D3 signature verification failed")?;

        let recomputed = calculate_rpc(&frame.symbols, domain as u32, self.epoch_nonce);
        if recomputed != frame.rpc_tag {
            return Err("RPC verification failed");
        }

        Ok(frame.symbols.clone())
    }
}

// Handshake state machine (from your file:133)
#[derive(Clone, Copy)]
pub struct Nonce(pub [u8; 32]);

pub struct Msg1 {
    pub nonce_i: Nonce,
}

pub struct Msg2 {
    pub nonce_r: Nonce,
    pub sig_r: Signature,
}

pub struct Msg3 {
    pub sig_i: Signature,
}

fn hash_nonces(prefix: &[u8], n1: &Nonce, n2: &Nonce) -> Vec<u8> {
    let mut h = Sha256::new();
    h.update(prefix);
    h.update(&n1.0);
    h.update(&n2.0);
    h.finalize().to_vec()
}

fn derive_epoch_nonce(n_i: &Nonce, n_r: &Nonce) -> u64 {
    let digest = hash_nonces(b"OTAP-EPOCH", n_i, n_r);
    let mut bytes = [0u8; 8];
    bytes.copy_from_slice(&digest[0..8]);
    u64::from_be_bytes(bytes)
}

pub struct Initiator {
    signing_key: SigningKey,
    peer_key: VerifyingKey,
}

pub struct InitiatorWaitMsg2 {
    signing_key: SigningKey,
    peer_key: VerifyingKey,
    nonce_i: Nonce,
}

pub struct Responder {
    signing_key: SigningKey,
    peer_key: VerifyingKey,
}

pub struct ResponderWaitMsg3 {
    signing_key: SigningKey,
    peer_key: VerifyingKey,
    nonce_i: Nonce,
    nonce_r: Nonce,
}

impl Initiator {
    pub fn new(signing_key: SigningKey, peer_key: VerifyingKey) -> Self {
        Self { signing_key, peer_key }
    }

    pub fn start(self) -> (InitiatorWaitMsg2, Msg1) {
        let mut n = [0u8; 32];
        OsRng.fill_bytes(&mut n);
        let nonce_i = Nonce(n);

        (
            InitiatorWaitMsg2 {
                signing_key: self.signing_key,
                peer_key: self.peer_key,
                nonce_i,
            },
            Msg1 { nonce_i },
        )
    }
}

impl Responder {
    pub fn new(signing_key: SigningKey, peer_key: VerifyingKey) -> Self {
        Self { signing_key, peer_key }
    }

    pub fn receive_msg1(self, msg1: Msg1) -> (ResponderWaitMsg3, Msg2) {
        let mut n = [0u8; 32];
        OsRng.fill_bytes(&mut n);
        let nonce_r = Nonce(n);

        let sig_r = self.signing_key.sign(&hash_nonces(b"HELLO-R", &msg1.nonce_i, &nonce_r));

        (
            ResponderWaitMsg3 {
                signing_key: self.signing_key,
                peer_key: self.peer_key,
                nonce_i: msg1.nonce_i,
                nonce_r,
            },
            Msg2 { nonce_r, sig_r },
        )
    }
}

impl InitiatorWaitMsg2 {
    pub fn receive_msg2(self, msg2: Msg2) -> Result<(D3Session, Msg3), &'static str> {
        self.peer_key
            .verify(
                &hash_nonces(b"HELLO-R", &self.nonce_i, &msg2.nonce_r),
                &msg2.sig_r,
            )
            .map_err(|_| "Invalid Sig(R)")?;

        let sig_i = self.signing_key.sign(&hash_nonces(b"HELLO-I", &msg2.nonce_r, &self.nonce_i));

        Ok((
            D3Session {
                epoch_nonce: derive_epoch_nonce(&self.nonce_i, &msg2.nonce_r),
                signing_key: self.signing_key,
                peer_key: self.peer_key,
            },
            Msg3 { sig_i },
        ))
    }
}

impl ResponderWaitMsg3 {
    pub fn receive_msg3(self, msg3: Msg3) -> Result<D3Session, &'static str> {
        self.peer_key
            .verify(
                &hash_nonces(b"HELLO-I", &self.nonce_r, &self.nonce_i),
                &msg3.sig_i,
            )
            .map_err(|_| "Invalid Sig(I)")?;

        Ok(D3Session {
            epoch_nonce: derive_epoch_nonce(&self.nonce_i, &self.nonce_r),
            signing_key: self.signing_key,
            peer_key: self.peer_key,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_handshake_and_secure_exchange() {
        let alice_sk = SigningKey::generate(&mut OsRng);
        let bob_sk = SigningKey::generate(&mut OsRng);

        // Handshake
        let (init_wait, m1) = Initiator::new(alice_sk.clone(), bob_sk.verifying_key()).start();
        let (resp_wait, m2) = Responder::new(bob_sk.clone(), alice_sk.verifying_key()).receive_msg1(m1);
        let (alice, m3) = init_wait.receive_msg2(m2).unwrap();
        let bob = resp_wait.receive_msg3(m3).unwrap();

        assert_eq!(alice.epoch_nonce, bob.epoch_nonce);

        // Data Exchange
        let payload = vec![Symbol {
            value: 1023,
            lambda: 1,
            pol: Polarization::H,
        }];

        let mut frame = alice.seal(RpcDomain::LinkData, payload).unwrap();
        assert!(bob.open(RpcDomain::LinkData, &frame).is_ok());

        // Forgery check
        frame.symbols[0].value = 0;
        frame.rpc_tag = calculate_rpc(&frame.symbols, RpcDomain::LinkData as u32, alice.epoch_nonce);
        assert!(bob.open(RpcDomain::LinkData, &frame).is_err());
    }
}
