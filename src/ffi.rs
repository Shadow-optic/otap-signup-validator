#[repr(u8)]
#[derive(Copy, Clone)]
pub enum CachePolicyFFI {
    Cached = 0,
    Uncached = 1,
    Streaming = 2,
}

#[link(name = "otap_mmio")]
extern "C" {
    fn otap_multilane_tx_ring_new(
        base_addrs: *const usize,
        lanes: u8,
        depth: u32,
        stride: u32,
    ) -> *mut libc::c_void;
    fn otap_multilane_submit_batch(
        ring: *mut libc::c_void,
        lane: u8,
        payloads: *const *const u8,
        count: usize,
    ) -> u32;
    fn otap_multilane_destroy(ring: *mut libc::c_void);
}

#[derive(Clone)]
pub struct OtapMultiLaneTxRing {
    ptr: *mut libc::c_void,
}

impl OtapMultiLaneTxRing {
    pub fn new(base_addrs: &[usize], lanes: u8, depth: u32, stride: u32) -> Self {
        let ptr = unsafe {
            otap_multilane_tx_ring_new(base_addrs.as_ptr(), lanes, depth, stride)
        };
        Self { ptr }
    }

    #[allow(non_snake_case)]
    pub fn submitBatch(&self, lane: u8, payloads: &[&[u8]]) -> u32 {
        let ptrs: Vec<*const u8> = payloads.iter().map(|p| p.as_ptr()).collect();
        unsafe {
            otap_multilane_submit_batch(self.ptr, lane, ptrs.as_ptr(), ptrs.len())
        }
    }
}

impl Drop for OtapMultiLaneTxRing {
    fn drop(&mut self) {
        // destroy handled by FFI; clones share ptr (caller-managed lifecycle)
    }
}

unsafe impl Send for OtapMultiLaneTxRing {}
unsafe impl Sync for OtapMultiLaneTxRing {}
