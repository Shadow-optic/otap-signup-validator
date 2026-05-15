//! OTAP Integration Layer
//!
//! Connects:
//! - BitWeave (Go) conflict detection
//! - MODQ (Rust) ring buffer
//! - D3 (Rust) crypto layer

use otap_modq::{ModqQueue, ModqConfig, TransportMode};
use otap_d3::{D3Session, Symbol, RpcDomain};
use std::sync::Mutex;
use std::ffi::CStr;
use std::os::raw::{c_char, c_int};

/// Global MODQ queue (thread-safe). Public so external tests can inject a queue.
pub static QUEUE: Mutex<Option<ModqQueue>> = Mutex::new(None);

/// Global D3 session (thread-safe)
static SESSION: Mutex<Option<D3Session>> = Mutex::new(None);

//  FFI: Initialize MODQ queue (called from Go)
#[no_mangle]
pub extern "C" fn otap_modq_init(
    shm_name: *const c_char,
    ring_depth: u32,
    slot_size: u32,
) -> c_int {
    let shm = unsafe {
        CStr::from_ptr(shm_name)
            .to_string_lossy()
            .into_owned()
    };

    let config = ModqConfig {
        transport: TransportMode::Loopback { shm_name: shm },
        ring_depth,
        slot_size,
    };

    match ModqQueue::open(config) {
        Ok(queue) => {
            *QUEUE.lock().unwrap() = Some(queue);
            0 // Success
        }
        Err(_) => -1, // Failure
    }
}

/// FFI: Submit payload to MODQ (called from Go after BitWeave check)
#[no_mangle]
pub extern "C" fn otap_modq_submit(
    payload: *const u8,
    len: usize,
) -> u32 {
    let data = unsafe { std::slice::from_raw_parts(payload, len) };

    let mut queue_guard = QUEUE.lock().unwrap();
    if let Some(ref mut queue) = *queue_guard {
        match queue.submit(data) {
            Ok(sid) => sid,
            Err(_) => 0xFFFFFFFF, // Error sentinel
        }
    } else {
        0xFFFFFFFF
    }
}

/// FFI: Seal payload with D3 crypto (called from Go)
#[no_mangle]
pub extern "C" fn otap_d3_seal(
    symbols_ptr: *const Symbol,
    symbols_len: usize,
    out_frame: *mut u8,
    out_len: *mut usize,
) -> c_int {
    let symbols = unsafe { std::slice::from_raw_parts(symbols_ptr, symbols_len) }.to_vec();

    let session_guard = SESSION.lock().unwrap();
    if let Some(ref session) = *session_guard {
        match session.seal(RpcDomain::LinkData, symbols) {
            Ok(frame) => {
                // Serialize frame to bytes (simplified)
                let serialized = serde_json::to_vec(&frame).unwrap();
                unsafe {
                    std::ptr::copy_nonoverlapping(
                        serialized.as_ptr(),
                        out_frame,
                        serialized.len(),
                    );
                    *out_len = serialized.len();
                }
                0
            }
            Err(_) => -1,
        }
    } else {
        -1
    }
}

// Helper for testing (not FFI)
pub fn test_pipeline(
    _channel: u32,
    _awg_id: u16,
    _tenant_id: u16,
    _awg_port: u16,
    payload: &[u8],
) -> Result<u32, String> {
    // 1. BitWeave check (via Go FFI - stub for now)
    let has_conflict = false; // TODO: Call Go bitweave_check_conflict()

    if has_conflict {
        return Err("Conflict detected".into());
    }

    // 2. Submit via MODQ
    let mut queue_guard = QUEUE.lock().unwrap();
    if let Some(ref mut queue) = *queue_guard {
        let sid = queue.submit(payload).map_err(|e| format!("{:?}", e))?;
        Ok(sid)
    } else {
        Err("MODQ not initialized".into())
    }
}
