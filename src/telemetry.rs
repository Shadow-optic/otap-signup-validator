use libc::{c_int, close};
use std::io;
use std::sync::atomic::{AtomicU64, Ordering};

pub struct PerfCounter {
    fd: c_int,
    value: AtomicU64,
}

impl PerfCounter {
    pub fn new(_hw_type: u32, _config: u64) -> Result<Self, io::Error> {
        Ok(Self {
            fd: -1,
            value: AtomicU64::new(0),
        })
    }

    pub fn enable(&self) {}

    pub fn disable(&self) {}

    pub fn read_value(&self) -> u64 {
        self.value.load(Ordering::Relaxed)
    }
}

impl Drop for PerfCounter {
    fn drop(&mut self) {
        if self.fd >= 0 {
            unsafe {
                close(self.fd);
            }
        }
    }
}
