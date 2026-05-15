use std::sync::atomic::{AtomicU64, Ordering};
use std::thread;
use crossbeam_utils::CachePadded;
use cudarc::driver::{CudaContext, CudaModule, CudaStream, LaunchConfig, CudaDevice};

mod telemetry;
mod ffi;
mod pcie;
mod cxl;
mod photonforge;
mod pfcp;
mod validator;

use telemetry::PerfCounter;
use ffi::OtapMultiLaneTxRing;
use cxl::CxlPool;
use photonforge::PhotonForgePFPGA;
use pfcp::PfcpCoherenceEngine;
use validator::SignupData;

const LANE_COUNT: usize = 80;
const GPU_COUNT: usize = 4;
const PAYLOAD_STRIDE: usize = 2032;
const CXL_POOL_SIZE: usize = 32 * 1024 * 1024 * 1024;

fn main() {
    println!("[OTAP] Starting 20 Tbps PhotonForge endpoint (v2.0)");

    let signup = SignupData {
        username: "operator-chi".to_string(),
        password: "p@$$w0rD123".to_string(),
        confirm_password: "p@$$w0rD123".to_string(),
    };
    match signup.validate_signup() {
        Ok(_) => println!("[Validator] Signup accepted"),
        Err(e) => println!("[Validator] Signup rejected: {}", e),
    }

    pcie::tune_pcie_mps_mrrs("/sys/bus/pci/devices/0000:01:00.0", 4096, 4096).unwrap();
    for gpu_id in 0..GPU_COUNT {
        pcie::enable_gpudirect_p2p(gpu_id, 0xDEADBEEF0000).unwrap();
    }

    let cxl_pool = CxlPool::new("/dev/dax0.0", CXL_POOL_SIZE, "POOL-CHI-NYC-001").unwrap();
    let fabric_ptr = cxl_pool.as_mut_ptr() as usize;

    let p_fpgas = vec![
        PhotonForgePFPGA::new(0, fabric_ptr, 20),
        PhotonForgePFPGA::new(1, fabric_ptr + 0x100000000, 20),
        PhotonForgePFPGA::new(2, fabric_ptr + 0x200000000, 20),
        PhotonForgePFPGA::new(3, fabric_ptr + 0x300000000, 20),
    ];

    let mut base_addrs = [0usize; LANE_COUNT];
    for (i, addr) in base_addrs.iter_mut().enumerate() {
        *addr = fabric_ptr + (i * 0x10000);
    }
    let rings = OtapMultiLaneTxRing::new(&base_addrs, LANE_COUNT as u8, 1024, 256);

    let cache_misses = PerfCounter::new(0, 0x03).unwrap();
    let branch_misses = PerfCounter::new(0, 0x05).unwrap();
    cache_misses.enable();
    branch_misses.enable();

    let mut handles = vec![];
    for lane in 0..LANE_COUNT {
        let chip_idx = lane / 20;
        let p_fpga = p_fpgas[chip_idx].clone();
        let pfcp_engine = PfcpCoherenceEngine::new(0xDEADBEEF0000, [0u8; 32]);
        let ring = rings.clone();
        let handle = thread::spawn(move || {
            let mut seq = 0u64;
            while seq < 10_000_000 / LANE_COUNT as u64 {
                let payload = b"OTAP_20TBPS_TRANSIENT";
                let payloads = &[payload];
                pfcp_engine.enforce_coherence(lane as u8, payload);
                let _sid = p_fpga.submit_batch(lane as u8, payloads);
                let _sid2 = ring.submitBatch(lane as u8, payloads);
                seq += 1;
            }
        });
        handles.push(handle);
    }
    for h in handles { h.join().unwrap(); }

    let cache_delta = cache_misses.read_value();
    let branch_delta = branch_misses.read_value();
    println!("[OTAP] 20 Tbps run complete");
    println!("[Telemetry] Cache misses: {} | Branch misses: {}", cache_delta, branch_delta);
}
