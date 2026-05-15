use std::hint::black_box;

pub struct PfcpCoherenceEngine {
    _bar_addr: u64,
    _key: [u8; 32],
}

impl PfcpCoherenceEngine {
    pub fn new(bar_addr: u64, key: [u8; 32]) -> Self {
        Self {
            _bar_addr: bar_addr,
            _key: key,
        }
    }

    pub fn enforce_coherence(&self, lane: u8, payload: &[u8]) {
        black_box((lane, payload.len()));
    }
}
