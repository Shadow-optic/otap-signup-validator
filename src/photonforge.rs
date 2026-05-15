use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

#[derive(Clone)]
pub struct PhotonForgePFPGA {
    _device_id: u8,
    _fabric_ptr: usize,
    _lanes: usize,
    sid: Arc<AtomicU64>,
}

impl PhotonForgePFPGA {
    pub fn new(device_id: u8, fabric_ptr: usize, lanes: usize) -> Self {
        Self {
            _device_id: device_id,
            _fabric_ptr: fabric_ptr,
            _lanes: lanes,
            sid: Arc::new(AtomicU64::new(1)),
        }
    }

    pub fn submit_batch(&self, _lane: u8, _payloads: &[&[u8]]) -> u64 {
        self.sid.fetch_add(1, Ordering::Relaxed)
    }
}
