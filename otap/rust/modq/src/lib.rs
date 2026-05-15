//! OTAP MODQ — Memory-Optical Direct Queue (integration shim)
//!
//! Thin, self-contained, in-memory loopback implementation of the MODQ ring
//! buffer surface used by the integration layer. Keeps the spec contract
//! `ModqQueue::open(config: ModqConfig) -> Result<ModqQueue, ModqError>` and
//! `queue.submit(&[u8]) -> Result<u32, ModqError>`.

use std::path::PathBuf;
use std::sync::Mutex;

/// Transport backend selection.
#[derive(Debug, Clone)]
pub enum TransportMode {
    PciBar { resource_path: PathBuf },
    Loopback { shm_name: String },
}

/// Configuration for opening a MODQ queue.
#[derive(Debug, Clone)]
pub struct ModqConfig {
    pub transport: TransportMode,
    pub ring_depth: u32,
    pub slot_size: u32,
}

/// MODQ queue handle. In this integration shim it is an in-process ring with
/// monotonically-increasing submission IDs (SIDs) — no kernel BAR mapping
/// required, so it works in any environment.
pub struct ModqQueue {
    config: ModqConfig,
    inner: Mutex<RingState>,
}

#[derive(Debug)]
struct RingState {
    next_sid: u32,
    submitted: u64,
    slots: Vec<Vec<u8>>,
}

#[derive(Debug, thiserror::Error)]
pub enum ModqError {
    #[error("ring depth must be a power of 2, got {0}")]
    InvalidRingDepth(u32),

    #[error("payload size {payload} exceeds slot size {slot}")]
    PayloadTooLarge { payload: usize, slot: u32 },

    #[error("invalid shared-memory name: {0}")]
    InvalidShmName(String),
}

impl ModqQueue {
    /// Open a MODQ queue. Takes the config by value to match the spec contract.
    pub fn open(config: ModqConfig) -> Result<Self, ModqError> {
        if !config.ring_depth.is_power_of_two() {
            return Err(ModqError::InvalidRingDepth(config.ring_depth));
        }

        if let TransportMode::Loopback { shm_name } = &config.transport {
            if shm_name.is_empty() {
                return Err(ModqError::InvalidShmName(shm_name.clone()));
            }
        }

        let depth = config.ring_depth as usize;
        let slots = (0..depth).map(|_| Vec::with_capacity(config.slot_size as usize)).collect();

        Ok(Self {
            config,
            inner: Mutex::new(RingState {
                next_sid: 0,
                submitted: 0,
                slots,
            }),
        })
    }

    /// Submit a payload. Returns the assigned submission-ID (SID).
    #[inline]
    pub fn submit(&mut self, payload: &[u8]) -> Result<u32, ModqError> {
        if payload.len() > self.config.slot_size as usize {
            return Err(ModqError::PayloadTooLarge {
                payload: payload.len(),
                slot: self.config.slot_size,
            });
        }

        let mut state = self.inner.lock().expect("modq ring poisoned");
        let sid = state.next_sid;
        let depth = self.config.ring_depth as usize;
        let slot_idx = (state.submitted as usize) % depth;
        state.slots[slot_idx].clear();
        state.slots[slot_idx].extend_from_slice(payload);
        state.next_sid = state.next_sid.wrapping_add(1);
        state.submitted = state.submitted.wrapping_add(1);
        Ok(sid)
    }

    pub fn ring_depth(&self) -> u32 {
        self.config.ring_depth
    }

    pub fn slot_size(&self) -> u32 {
        self.config.slot_size
    }

    pub fn submitted_count(&self) -> u64 {
        self.inner.lock().expect("modq ring poisoned").submitted
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_non_power_of_two_depth() {
        let cfg = ModqConfig {
            transport: TransportMode::Loopback { shm_name: "/t".into() },
            ring_depth: 100,
            slot_size: 64,
        };
        assert!(matches!(ModqQueue::open(cfg), Err(ModqError::InvalidRingDepth(100))));
    }

    #[test]
    fn submits_and_increments_sid() {
        let cfg = ModqConfig {
            transport: TransportMode::Loopback { shm_name: "/t".into() },
            ring_depth: 8,
            slot_size: 64,
        };
        let mut q = ModqQueue::open(cfg).unwrap();
        assert_eq!(q.submit(b"hello").unwrap(), 0);
        assert_eq!(q.submit(b"world").unwrap(), 1);
        assert_eq!(q.submitted_count(), 2);
    }

    #[test]
    fn rejects_oversize_payload() {
        let cfg = ModqConfig {
            transport: TransportMode::Loopback { shm_name: "/t".into() },
            ring_depth: 4,
            slot_size: 4,
        };
        let mut q = ModqQueue::open(cfg).unwrap();
        assert!(matches!(q.submit(&[0u8; 8]), Err(ModqError::PayloadTooLarge { .. })));
    }
}
