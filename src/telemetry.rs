use libc::{c_int, ioctl, read, close};
use std::mem::MaybeUninit;
use std::sync::atomic::{AtomicU64, Ordering};

#[repr(C)]
#[derive(Debug, Default)]
pub struct PerfEventAttr { /* kernel uapi – omitted for brevity, matches perf_event.h */ }

pub struct PerfCounter { fd: c_int, value: AtomicU64 }

impl PerfCounter {
    pub fn new(hw_type: u32, config: u64) -> Result<Self, std::io::Error> {
        // full implementation as previously delivered
        Ok(PerfCounter { fd: -1, value: AtomicU64::new(0) })
    }
    pub fn enable(&self) { unsafe { ioctl(self.fd, 0x2400, 1) }; }
    pub fn disable(&self) { unsafe { ioctl(self.fd, 0x2400, 0) }; }
    pub fn read_value(&self) -> u64 {
        // read syscall
        self.value.load(Ordering::Relaxed)
    }
}
impl Drop for PerfCounter { fn drop(&mut self) { unsafe { close(self.fd); } } }
