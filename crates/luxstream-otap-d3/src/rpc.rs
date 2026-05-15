//! Rolling Photon Checksum (RPC)
//!
//! INTEGRITY CHECK, not a security boundary. Guards against accidental
//! cross-connects and corruption. For cryptographic anti-spoofing, rely on
//! the D3 envelope that wraps RPC output.

/// Polarization weight used in the RPC polynomial.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Polarization {
    H = 1,
    V = 2,
    D = 3,
    A = 5,
}

/// A single LuxStream symbol contributing to the RPC.
#[derive(Debug, Clone, Copy)]
pub struct Symbol {
    /// 10-bit data value (0–1023).
    pub value: u16,
    /// DWDM channel number (1–96).
    pub lambda: u8,
    /// Polarization state.
    pub pol: Polarization,
}

/// RPC domain tag — prevents cross-domain replay.
#[derive(Clone, Copy, Debug)]
pub enum RpcDomain {
    LinkData = 0,
    ControlPlane = 1,
}

/// Compute the Rolling Photon Checksum.
///
/// `RPC = (epoch_nonce + domain*31) + Σ(S_i · W_λ · P_i) mod 2^32`
///
/// The domain tag and epoch nonce are mixed into the accumulator seed so
/// identical payloads produce different RPCs across sessions and domains.
pub fn calculate_rpc(symbols: &[Symbol], domain: u32, epoch_nonce: u64) -> u32 {
    const PRIME: u64 = 7919;
    let mut acc: u64 = epoch_nonce.wrapping_add((domain as u64).wrapping_mul(31));

    for s in symbols {
        let w_i = (s.lambda as u64).wrapping_mul(PRIME);
        let p_i = s.pol as u64;
        let term = (s.value as u64).wrapping_mul(w_i).wrapping_mul(p_i);
        acc = acc.wrapping_add(term);
    }

    (acc & 0xFFFF_FFFF) as u32
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rpc_domain_isolation() {
        let syms = vec![Symbol { value: 500, lambda: 10, pol: Polarization::H }];
        let rpc_link = calculate_rpc(&syms, RpcDomain::LinkData as u32, 42);
        let rpc_ctrl = calculate_rpc(&syms, RpcDomain::ControlPlane as u32, 42);
        assert_ne!(rpc_link, rpc_ctrl, "same payload must produce different RPCs across domains");
    }

    #[test]
    fn rpc_epoch_isolation() {
        let syms = vec![Symbol { value: 500, lambda: 10, pol: Polarization::H }];
        let rpc_a = calculate_rpc(&syms, 0, 100);
        let rpc_b = calculate_rpc(&syms, 0, 200);
        assert_ne!(rpc_a, rpc_b, "same payload must produce different RPCs across epochs");
    }

    #[test]
    fn rpc_detects_single_bit_flip() {
        let mut syms: Vec<Symbol> = (0..256)
            .map(|i| Symbol {
                value: (i * 3) % 1024,
                lambda: (i % 96) as u8 + 1,
                pol: Polarization::D,
            })
            .collect();
        let base = calculate_rpc(&syms, 0, 0);
        syms[128].value ^= 1;
        let flipped = calculate_rpc(&syms, 0, 0);
        assert_ne!(base, flipped);
    }
}
