use otap_bench::batch::BatchRingBuffer;

#[test]
fn test_batch_submit() {
    let batch: BatchRingBuffer<u64, 256> = BatchRingBuffer::new(64);
    let items: Vec<u64> = (0..100).collect();
    let submitted = batch.submit_batch(&items);
    assert_eq!(submitted, 100);

    // Drain and verify
    let mut out = [0u64; 256];
    let drained = batch.drain_batch(&mut out);
    assert_eq!(drained, 100);
    for i in 0..100 {
        assert_eq!(out[i], i as u64);
    }
}

#[test]
fn test_batch_partial_fill() {
    let batch: BatchRingBuffer<u64, 16> = BatchRingBuffer::new(8);
    // Capacity = 15 usable slots
    let items: Vec<u64> = (0..20).collect();
    let submitted = batch.submit_batch(&items);
    assert_eq!(submitted, 15); // ring capacity limit
}

#[test]
fn test_batch_amortization() {
    // Verify that batch submit uses fewer cursor updates than individual
    // (This is a structural test — we can't count atomics directly)
    let batch: BatchRingBuffer<u64, 1024> = BatchRingBuffer::new(64);
    let items: Vec<u64> = (0..640).collect();
    let submitted = batch.submit_batch(&items);
    assert_eq!(submitted, 640);

    // Drain in batches of 64
    let mut total_drained = 0;
    let mut out = [0u64; 64];
    loop {
        let n = batch.drain_batch(&mut out);
        if n == 0 {
            break;
        }
        total_drained += n;
    }
    assert_eq!(total_drained, 640);
}
