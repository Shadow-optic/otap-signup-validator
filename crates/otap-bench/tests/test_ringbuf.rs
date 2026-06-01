use otap_bench::ringbuf::RingBuffer;

#[test]
fn test_ringbuf_basic_submit_drain() {
    // Create ring, submit 100 items, drain all, verify order
    let ring: RingBuffer<u64, 256> = RingBuffer::new();
    for i in 0..100u64 {
        assert!(ring.try_submit(i), "submit {} failed", i);
    }
    for i in 0..100u64 {
        assert_eq!(ring.try_drain(), Some(i), "drain {} failed", i);
    }
}

#[test]
fn test_ringbuf_capacity() {
    // RingBuffer<_, 256> has 255 usable slots
    let ring: RingBuffer<u64, 256> = RingBuffer::new();
    assert_eq!(ring.capacity(), 255);

    // Fill to capacity
    for i in 0..255u64 {
        assert!(ring.try_submit(i));
    }
    // 256th should fail (ring full)
    assert!(!ring.try_submit(999));
}

#[test]
fn test_ringbuf_empty() {
    let ring: RingBuffer<u64, 256> = RingBuffer::new();
    assert_eq!(ring.try_drain(), None);
    assert_eq!(ring.readable(), 0);
}

#[test]
fn test_ringbuf_wraparound() {
    // Fill, drain half, fill more to force wraparound
    let ring: RingBuffer<u64, 16> = RingBuffer::new();
    // Fill 15 slots (capacity of ring-16)
    for i in 0..15u64 {
        assert!(ring.try_submit(i));
    }
    // Drain 8
    for i in 0..8u64 {
        assert_eq!(ring.try_drain(), Some(i));
    }
    // Submit 8 more (should wrap)
    for i in 100..108u64 {
        assert!(ring.try_submit(i));
    }
    // Drain remaining
    for i in 8..15u64 {
        assert_eq!(ring.try_drain(), Some(i));
    }
    for i in 100..108u64 {
        assert_eq!(ring.try_drain(), Some(i));
    }
    assert_eq!(ring.try_drain(), None);
}

#[test]
fn test_ringbuf_concurrent_spsc() {
    // Single producer, single consumer — stress test
    use std::sync::Arc;
    use std::thread;

    let ring: Arc<RingBuffer<u64, 1024>> = Arc::new(RingBuffer::new());
    let ring_p = Arc::clone(&ring);
    let ring_c = Arc::clone(&ring);

    let producer = thread::spawn(move || {
        for i in 0..10_000u64 {
            while !ring_p.try_submit(i) {
                thread::yield_now();
            }
        }
    });

    let consumer = thread::spawn(move || {
        let mut received = Vec::new();
        while received.len() < 10_000 {
            if let Some(v) = ring_c.try_drain() {
                received.push(v);
            } else {
                thread::yield_now();
            }
        }
        received
    });

    producer.join().unwrap();
    let received = consumer.join().unwrap();

    assert_eq!(received.len(), 10_000);
    for (i, &v) in received.iter().enumerate() {
        assert_eq!(v, i as u64, "out of order at index {}", i);
    }
}

#[test]
fn test_ringbuf_available_readable() {
    let ring: RingBuffer<u64, 256> = RingBuffer::new();
    assert_eq!(ring.available(), 255);
    assert_eq!(ring.readable(), 0);

    ring.try_submit(1);
    assert_eq!(ring.available(), 254);
    assert_eq!(ring.readable(), 1);
}
