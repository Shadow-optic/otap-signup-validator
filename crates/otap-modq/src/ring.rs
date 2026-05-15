//! Lock-free SPSC ring buffer mapped to FPGA BAR memory.

use std::sync::Arc;
use crate::bar::{self, BarMapping};

pub const SLOT_FREE: u32 = 0;
pub const SLOT_READY: u32 = 1;
pub const SLOT_DONE: u32 = 2;

const SLOT_HDR_STATUS: usize = 0x00;
const SLOT_HDR_LENGTH: usize = 0x04;
const SLOT_HDR_SID: usize = 0x08;
const SLOT_PAYLOAD_OFFSET: usize = 0x10;

pub struct TxRing {
    bar: Arc<BarMapping>,
    depth: u32,
    slot_stride: u32,
    tail: u64,
    next_sid: u32,
}

pub struct CompletionRing {
    bar: Arc<BarMapping>,
    depth: u32,
    slot_stride: u32,
    head: u64,
}

#[derive(Debug, Clone)]
pub struct Completion {
    pub sequence_id: u32,
    pub slot_index: usize,
}

impl TxRing {
    pub fn new(bar: Arc<BarMapping>) -> Self {
        let depth = bar.read32(bar::regs::DEPTH);
        let slot_size = bar.read32(bar::regs::SLOT_SIZE);
        let raw_stride = SLOT_PAYLOAD_OFFSET as u32 + slot_size;
        let slot_stride = (raw_stride + 63) & !63;

        Self { bar, depth, slot_stride, tail: 0, next_sid: 0 }
    }

    pub fn with_params(bar: Arc<BarMapping>, depth: u32, slot_size: u32) -> Self {
        let raw_stride = SLOT_PAYLOAD_OFFSET as u32 + slot_size;
        let slot_stride = (raw_stride + 63) & !63;

        Self { bar, depth, slot_stride, tail: 0, next_sid: 0 }
    }

    pub fn submit(&mut self, payload: &[u8]) -> Result<u32, RingError> {
        let max_payload = (self.slot_stride as usize) - SLOT_PAYLOAD_OFFSET;
        if payload.len() > max_payload {
            return Err(RingError::PayloadTooLarge { got: payload.len(), max: max_payload });
        }

        let index = (self.tail % self.depth as u64) as usize;
        let slot_base = bar::regs::RING_BASE + index * self.slot_stride as usize;

        let mut spins = 0u64;
        loop {
            let status = self.bar.read32(slot_base + SLOT_HDR_STATUS);
            if status == SLOT_FREE { break; }
            spin_hint();
            spins += 1;
            if spins > 1_000_000_000 {
                return Err(RingError::Timeout { slot: index });
            }
        }

        let payload_ptr = self.bar.slot_ptr(slot_base + SLOT_PAYLOAD_OFFSET);
        unsafe {
            std::ptr::copy_nonoverlapping(payload.as_ptr(), payload_ptr, payload.len());
        }

        let sid = self.next_sid;
        self.bar.write32(slot_base + SLOT_HDR_LENGTH, payload.len() as u32);
        self.bar.write32(slot_base + SLOT_HDR_SID, sid);

        std::sync::atomic::fence(std::sync::atomic::Ordering::Release);

        self.bar.write32(slot_base + SLOT_HDR_STATUS, SLOT_READY);

        self.tail += 1;
        self.next_sid = self.next_sid.wrapping_add(1);
        self.bar.write32(bar::regs::DOORBELL, self.tail as u32);

        Ok(sid)
    }

    #[inline(always)]
    pub unsafe fn submit_unchecked(&mut self, payload: *const u8, len: usize) -> u32 {
        let index = (self.tail % self.depth as u64) as usize;
        let slot_base = bar::regs::RING_BASE + index * self.slot_stride as usize;

        while self.bar.read32(slot_base + SLOT_HDR_STATUS) != SLOT_FREE {
            spin_hint();
        }

        let dest = self.bar.slot_ptr(slot_base + SLOT_PAYLOAD_OFFSET);
        std::ptr::copy_nonoverlapping(payload, dest, len);

        let sid = self.next_sid;
        self.bar.write32(slot_base + SLOT_HDR_LENGTH, len as u32);
        self.bar.write32(slot_base + SLOT_HDR_SID, sid);
        std::sync::atomic::fence(std::sync::atomic::Ordering::Release);
        self.bar.write32(slot_base + SLOT_HDR_STATUS, SLOT_READY);

        self.tail += 1;
        self.next_sid = self.next_sid.wrapping_add(1);
        self.bar.write32(bar::regs::DOORBELL, self.tail as u32);

        sid
    }

    pub fn slot_payload_capacity(&self) -> usize {
        self.slot_stride as usize - SLOT_PAYLOAD_OFFSET
    }

    pub fn depth(&self) -> u32 { self.depth }
    pub fn tail(&self) -> u64 { self.tail }
}

impl CompletionRing {
    pub fn new(bar: Arc<BarMapping>, depth: u32, slot_stride: u32) -> Self {
        Self { bar, depth, slot_stride, head: 0 }
    }

    pub fn poll(&mut self) -> Option<Completion> {
        let index = (self.head % self.depth as u64) as usize;
        let slot_base = bar::regs::RING_BASE + index * self.slot_stride as usize;

        let status = self.bar.read32(slot_base + SLOT_HDR_STATUS);
        if status == SLOT_DONE {
            let sid = self.bar.read32(slot_base + SLOT_HDR_SID);
            self.bar.write32(slot_base + SLOT_HDR_STATUS, SLOT_FREE);
            self.head += 1;
            Some(Completion { sequence_id: sid, slot_index: index })
        } else {
            None
        }
    }
}

#[derive(Debug, thiserror::Error)]
pub enum RingError {
    #[error("payload too large: {got} bytes, max {max}")]
    PayloadTooLarge { got: usize, max: usize },

    #[error("timeout waiting for slot {slot} to become free")]
    Timeout { slot: usize },
}

#[inline(always)]
fn spin_hint() {
    #[cfg(target_arch = "x86_64")]
    unsafe { std::arch::x86_64::_mm_pause(); }
    #[cfg(target_arch = "aarch64")]
    unsafe { std::arch::aarch64::__yield(); }
    #[cfg(not(any(target_arch = "x86_64", target_arch = "aarch64")))]
    std::hint::spin_loop();
}
