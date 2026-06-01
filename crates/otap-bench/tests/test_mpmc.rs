use otap_bench::mpmc::WorkStealingQueue;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::thread;

#[test]
fn test_mpmc_basic() {
    let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(2);

    // Submit 100 items
    for i in 0..100u64 {
        assert!(queue.submit(i));
    }

    // Two consumers drain all
    let q1 = Arc::new(queue);
    let q2 = Arc::clone(&q1);
    let count = Arc::new(AtomicUsize::new(0));
    let count1 = Arc::clone(&count);
    let count2 = Arc::clone(&count);

    let h1 = thread::spawn(move || {
        let mut local_count = 0;
        for _ in 0..60 {
            // may get more or less
            if q1.steal(0).is_some() {
                local_count += 1;
            }
        }
        count1.fetch_add(local_count, Ordering::Relaxed);
    });

    let h2 = thread::spawn(move || {
        let mut local_count = 0;
        for _ in 0..60 {
            if q2.steal(1).is_some() {
                local_count += 1;
            }
        }
        count2.fetch_add(local_count, Ordering::Relaxed);
    });

    h1.join().unwrap();
    h2.join().unwrap();

    let total = count.load(Ordering::Relaxed);
    assert_eq!(total, 100, "Expected 100 items drained, got {}", total);
}

#[test]
fn test_mpmc_steal_ordering() {
    // Items should not be lost or duplicated
    let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(4);

    for i in 0..1000u64 {
        queue.submit(i);
    }

    let q = Arc::new(queue);
    let mut handles = vec![];
    let results: Arc<[AtomicUsize; 1000]> =
        Arc::new(std::array::from_fn(|_| AtomicUsize::new(0)));

    for worker_id in 0..4 {
        let q = Arc::clone(&q);
        let results = Arc::clone(&results);
        handles.push(thread::spawn(move || {
            loop {
                match q.steal(worker_id) {
                    Some(v) if (v as usize) < 1000 => {
                        results[v as usize].fetch_add(1, Ordering::Relaxed);
                    }
                    Some(_) => break,
                    None => break,
                }
            }
        }));
    }

    for h in handles {
        h.join().unwrap();
    }

    // Each item should be drained exactly once
    for i in 0..1000 {
        let count = results[i].load(Ordering::Relaxed);
        assert_eq!(count, 1, "item {} was drained {} times", i, count);
    }
}

#[test]
fn test_mpmc_empty_queue() {
    let queue: WorkStealingQueue<u64> = WorkStealingQueue::new(2);
    assert!(queue.is_empty());
    assert_eq!(queue.steal(0), None);
    assert_eq!(queue.steal(1), None);
}
