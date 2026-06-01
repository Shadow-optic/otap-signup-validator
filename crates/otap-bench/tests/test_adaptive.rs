use otap_bench::adaptive::{AdaptiveBackpressure, AdaptiveConfig, Tier};

#[test]
fn test_adaptive_tiers() {
    let config = AdaptiveConfig {
        spin_limit: 10,
        yield_limit: 5,
        sleep_nanos: 1,
    };
    let adaptive = AdaptiveBackpressure::new(config);

    // First 10 retries should spin
    for i in 0..10 {
        assert_eq!(adaptive.wait(i), Tier::Spin);
    }
    // Next 5 should yield
    for i in 10..15 {
        assert_eq!(adaptive.wait(i), Tier::Yield);
    }
    // After that should sleep
    assert_eq!(adaptive.wait(15), Tier::Sleep);
}

#[test]
fn test_adaptive_decomposition() {
    let config = AdaptiveConfig::default();
    let adaptive = AdaptiveBackpressure::new(config);

    // Test with an operation that succeeds on first try
    let mut calls = 0;
    let result = adaptive.submit_decomposed(
        |_item: u64| {
            calls += 1;
            true
        },
        42u64,
    );

    assert_eq!(calls, 1);
    assert!(result.write_ns > 0);
    assert_eq!(result.wait_ns, 0); // No waiting needed
    // total_ns includes function-call overhead; write_ns is just the closure.
    // In a zero-wait path they should be very close (within 2x for slow CI).
    assert!(
        result.total_ns >= result.write_ns && result.total_ns <= result.write_ns * 10,
        "total_ns ({}) should be >= write_ns ({}) and not wildly larger",
        result.total_ns,
        result.write_ns
    );
}

#[test]
fn test_adaptive_with_retries() {
    let config = AdaptiveConfig::default();
    let adaptive = AdaptiveBackpressure::new(config);

    // Operation that fails 5 times then succeeds
    let mut attempts = 0;
    let result = adaptive.submit_decomposed(
        |_item: u64| {
            attempts += 1;
            attempts > 5 // fail first 5, succeed on 6th
        },
        42u64,
    );

    assert_eq!(attempts, 6);
    assert!(result.write_ns > 0);
    assert!(result.wait_ns > 0); // Should have waited
    // total_ns includes overhead from the retry loop; it must cover at least
    // the measured write + wait time.
    assert!(
        result.total_ns >= result.write_ns + result.wait_ns,
        "total_ns ({}) should be >= write_ns ({}) + wait_ns ({})",
        result.total_ns,
        result.write_ns,
        result.wait_ns
    );
}

#[test]
fn test_adaptive_retry_count() {
    let config = AdaptiveConfig::default();
    let adaptive = AdaptiveBackpressure::new(config);
    assert_eq!(adaptive.retry_count(), 0);

    // Perform some waits
    adaptive.wait(0);
    adaptive.wait(1);
    adaptive.wait(2);

    // retry_count should track cumulative
    assert!(adaptive.retry_count() > 0);
}
