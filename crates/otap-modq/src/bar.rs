//! PCIe BAR memory-mapped register access for Corundum FPGA.
//!
//! Maps the FPGA's BAR2 region into user-space via `/sys/bus/pci/devices/.../resource2`.
//! All register reads/writes are volatile MMIO — no caching, no kernel involvement.

use std::path::Path;
use std::fs::OpenOptions;
use std::os::unix::io::AsRawFd;

use crate::ModqError;

/// Memory-mapped PCIe BAR region.
pub struct BarMapping {
    base: *mut u8,
    size: usize,
}

// MMIO pointers are inherently shared between host and device.
unsafe impl Send for BarMapping {}
unsafe impl Sync for BarMapping {}

/// Register offsets within the OTAP application block's BAR region.
/// These must match the CSR address map in `otap_app_core.v`.
pub mod regs {
    /// Ring head pointer (read-only from host; FPGA increments after consuming a slot).
    pub const HEAD: usize = 0x0000;
    /// Ring tail pointer (host increments after writing a slot; FPGA reads).
    pub const TAIL: usize = 0x0008;
    /// Ring depth (number of slots). Set at init, read-only thereafter.
    pub const DEPTH: usize = 0x0010;
    /// Slot payload size in bytes. Set at init, read-only thereafter.
    pub const SLOT_SIZE: usize = 0x0018;
    /// Doorbell register (write-only). Host writes tail value to notify FPGA.
    pub const DOORBELL: usize = 0x0040;
    /// Ingress sequence ID counter (read-only). Mirrors `ingress_sid` in RTL.
    pub const INGRESS_SID: usize = 0x0048;
    /// Commit pointer (read-only). Mirrors ROB `commit_ptr` in RTL.
    pub const COMMIT_PTR: usize = 0x0050;
    /// Hardware cycle counter (read-only). FPGA clock cycles since reset.
    pub const HW_CYCLE_COUNT: usize = 0x0058;
    /// Packets dropped by FPGA due to backpressure (read-only).
    pub const HW_DROP_COUNT: usize = 0x0060;
    /// Start of the ring buffer slot region.
    pub const RING_BASE: usize = 0x1000;
}

impl BarMapping {
    /// Map a PCIe BAR resource file into user-space.
    ///
    /// `resource_path` is typically `/sys/bus/pci/devices/0000:XX:XX.X/resource2`
    /// for Corundum's BAR2.
    pub fn open<P: AsRef<Path>>(resource_path: P) -> Result<Self, ModqError> {
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .open(resource_path.as_ref())
            .map_err(|e| ModqError::BarOpen {
                path: resource_path.as_ref().display().to_string(),
                source: e,
            })?;

        // Determine BAR size from file metadata.
        let metadata = file.metadata().map_err(|e| ModqError::BarOpen {
            path: resource_path.as_ref().display().to_string(),
            source: e,
        })?;
        let size = metadata.len() as usize;

        if size == 0 {
            return Err(ModqError::BarEmpty {
                path: resource_path.as_ref().display().to_string(),
            });
        }

        let fd = file.as_raw_fd();

        let base = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                size,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                fd,
                0,
            )
        };

        if base == libc::MAP_FAILED {
            return Err(ModqError::MmapFailed {
                path: resource_path.as_ref().display().to_string(),
            });
        }

        Ok(Self {
            base: base as *mut u8,
            size,
        })
    }

    /// Create a BAR mapping from a POSIX shared memory region (for loopback testing
    /// without real FPGA hardware). The shared memory object is created if it doesn't exist.
    pub fn open_shm(name: &str, size: usize) -> Result<Self, ModqError> {
        let c_name = std::ffi::CString::new(name)
            .map_err(|_| ModqError::InvalidShmName(name.to_string()))?;

        let fd = unsafe {
            libc::shm_open(c_name.as_ptr(), libc::O_CREAT | libc::O_RDWR, 0o666)
        };
        if fd < 0 {
            return Err(ModqError::ShmOpenFailed(name.to_string()));
        }

        unsafe {
            if libc::ftruncate(fd, size as libc::off_t) < 0 {
                libc::close(fd);
                return Err(ModqError::ShmTruncateFailed(name.to_string()));
            }
        }

        let base = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                size,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                fd,
                0,
            )
        };

        unsafe { libc::close(fd) };

        if base == libc::MAP_FAILED {
            return Err(ModqError::MmapFailed {
                path: format!("shm:{}", name),
            });
        }

        Ok(Self {
            base: base as *mut u8,
            size,
        })
    }

    /// Volatile 32-bit MMIO read at the given byte offset.
    #[inline(always)]
    pub fn read32(&self, offset: usize) -> u32 {
        assert!(offset + 4 <= self.size, "BAR read out of bounds");
        unsafe { std::ptr::read_volatile(self.base.add(offset) as *const u32) }
    }

    /// Volatile 64-bit MMIO read at the given byte offset.
    #[inline(always)]
    pub fn read64(&self, offset: usize) -> u64 {
        assert!(offset + 8 <= self.size, "BAR read out of bounds");
        unsafe { std::ptr::read_volatile(self.base.add(offset) as *const u64) }
    }

    /// Volatile 32-bit MMIO write at the given byte offset.
    #[inline(always)]
    pub fn write32(&self, offset: usize, value: u32) {
        assert!(offset + 4 <= self.size, "BAR write out of bounds");
        unsafe { std::ptr::write_volatile(self.base.add(offset) as *mut u32, value) }
    }

    /// Volatile 64-bit MMIO write at the given byte offset.
    #[inline(always)]
    pub fn write64(&self, offset: usize, value: u64) {
        assert!(offset + 8 <= self.size, "BAR write out of bounds");
        unsafe { std::ptr::write_volatile(self.base.add(offset) as *mut u64, value) }
    }

    /// Raw pointer to a byte offset within the BAR. Used for bulk payload copies.
    #[inline(always)]
    pub fn slot_ptr(&self, offset: usize) -> *mut u8 {
        assert!(offset < self.size, "BAR slot_ptr out of bounds");
        unsafe { self.base.add(offset) }
    }

    /// Total mapped size in bytes.
    pub fn size(&self) -> usize {
        self.size
    }
}

impl Drop for BarMapping {
    fn drop(&mut self) {
        if !self.base.is_null() {
            unsafe {
                libc::munmap(self.base as *mut libc::c_void, self.size);
            }
        }
    }
}
