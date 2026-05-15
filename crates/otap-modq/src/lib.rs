//! # OTAP MODQ — Memory-Optical Direct Queue
//!
//! Zero-copy, kernel-bypass interface between host applications and the OTAP FPGA
//! endpoint.

pub mod bar;
pub mod ring;

use bar::BarMapping;
use ring::{TxRing, CompletionRing, Completion, RingError};
use std::path::PathBuf;
use std::sync::Arc;

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

/// A MODQ queue handle. Provides zero-copy submission and completion polling.
pub struct ModqQueue {
    bar: Arc<BarMapping>,
    tx: TxRing,
    completions: CompletionRing,
    config: ModqConfig,
}

impl ModqQueue {
    /// Open a MODQ queue with the given configuration.
    pub fn open(config: &ModqConfig) -> Result<Self, ModqError> {
        if !config.ring_depth.is_power_of_two() {
            return Err(ModqError::InvalidRingDepth(config.ring_depth));
        }

        let slot_stride = ((config.slot_size + 16 + 63) & !63) as usize;
        let ring_region = slot_stride * config.ring_depth as usize;
        let total_size = bar::regs::RING_BASE + ring_region;

        let bar_mapping = match &config.transport {
            TransportMode::PciBar { resource_path } => {
                BarMapping::open(resource_path)?
            }
            TransportMode::Loopback { shm_name } => {
                let mapping = BarMapping::open_shm(shm_name, total_size)?;

                mapping.write32(bar::regs::DEPTH, config.ring_depth);
                mapping.write32(bar::regs::SLOT_SIZE, config.slot_size);
                mapping.write64(bar::regs::HEAD, 0);
                mapping.write64(bar::regs::TAIL, 0);

                for i in 0..config.ring_depth as usize {
                    let slot_base = bar::regs::RING_BASE + i * slot_stride;
                    mapping.write32(slot_base, ring::SLOT_FREE);
                }

                mapping
            }
        };

        let bar = Arc::new(bar_mapping);

        let tx = TxRing::with_params(bar.clone(), config.ring_depth, config.slot_size);
        let completions = CompletionRing::new(
            bar.clone(),
            config.ring_depth,
            slot_stride as u32,
        );

        Ok(Self {
            bar,
            tx,
            completions,
            config: config.clone(),
        })
    }

    #[inline]
    pub fn submit(&mut self, payload: &[u8]) -> Result<u32, RingError> {
        self.tx.submit(payload)
    }

    #[inline(always)]
    pub unsafe fn submit_unchecked(&mut self, payload: *const u8, len: usize) -> u32 {
        self.tx.submit_unchecked(payload, len)
    }

    #[inline]
    pub fn poll(&mut self) -> Option<Completion> {
        self.completions.poll()
    }

    pub fn slot_payload_capacity(&self) -> usize {
        self.tx.slot_payload_capacity()
    }

    pub fn ring_depth(&self) -> u32 {
        self.config.ring_depth
    }

    pub fn submitted_count(&self) -> u64 {
        self.tx.tail()
    }

    pub fn fpga_ingress_sid(&self) -> u32 {
        self.bar.read32(bar::regs::INGRESS_SID)
    }

    pub fn fpga_commit_ptr(&self) -> u32 {
        self.bar.read32(bar::regs::COMMIT_PTR)
    }

    /// Read the FPGA's hardware cycle counter (clock cycles since reset).
    /// Bypasses OS timers — direct BAR register read.
    pub fn hardware_timestamp(&self) -> u64 {
        self.bar.read64(bar::regs::HW_CYCLE_COUNT)
    }

    /// Read the FPGA's hardware drop counter (packets lost to backpressure).
    /// Zero means zero drops at the hardware level.
    pub fn hardware_drop_count(&self) -> u32 {
        self.bar.read32(bar::regs::HW_DROP_COUNT)
    }
}

/// Errors from MODQ operations.
#[derive(Debug, thiserror::Error)]
pub enum ModqError {
    #[error("failed to open BAR resource at {path}: {source}")]
    BarOpen { path: String, #[source] source: std::io::Error },

    #[error("BAR resource at {path} has zero size — device may not be enumerated")]
    BarEmpty { path: String },

    #[error("mmap failed for {path}")]
    MmapFailed { path: String },

    #[error("ring depth must be a power of 2, got {0}")]
    InvalidRingDepth(u32),

    #[error("invalid shared memory name: {0}")]
    InvalidShmName(String),

    #[error("failed to open shared memory: {0}")]
    ShmOpenFailed(String),

    #[error("failed to truncate shared memory: {0}")]
    ShmTruncateFailed(String),
}
