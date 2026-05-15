use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

#[derive(Clone)]
pub struct OtapMultiLaneTxRing {
    _base_addrs: Arc<Vec<usize>>,
    _lane_count: u8,
    _queue_depth: u16,
    _burst: u16,
    sid: Arc<AtomicU64>,
}

impl OtapMultiLaneTxRing {
    pub fn new(base_addrs: &[usize], lane_count: u8, queue_depth: u16, burst: u16) -> Self {
        Self {
            _base_addrs: Arc::new(base_addrs.to_vec()),
            _lane_count: lane_count,
            _queue_depth: queue_depth,
            _burst: burst,
            sid: Arc::new(AtomicU64::new(1)),
        }
    }

    #[allow(non_snake_case)]
    pub fn submitBatch(&self, _lane: u8, _payloads: &[&[u8]]) -> u64 {
        self.sid.fetch_add(1, Ordering::Relaxed)
    }
}
