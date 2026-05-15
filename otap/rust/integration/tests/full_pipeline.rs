use otap_integration::test_pipeline;
use otap_modq::{ModqQueue, ModqConfig, TransportMode};

#[test]
fn test_full_otap_pipeline() {
    // Initialize MODQ
    let config = ModqConfig {
        transport: TransportMode::Loopback {
            shm_name: "/otap-test".into(),
        },
        ring_depth: 256,
        slot_size: 2048,
    };

    let queue = ModqQueue::open(config).expect("MODQ init failed");

    // Initialize global queue (safe — Mutex handles synchronization).
    otap_integration::QUEUE.lock().unwrap().replace(queue);

    // Test pipeline
    let payload = b"test payload data";
    let sid = test_pipeline(
        42,   // channel
        1,    // awg_id
        100,  // tenant_id
        7,    // awg_port
        payload,
    ).expect("Pipeline failed");

    println!("✓ Pipeline test passed, SID: {}", sid);
}
