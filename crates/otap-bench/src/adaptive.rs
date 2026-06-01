//! Adaptive backpressure with latency decomposition.
//!
//! # Novel Innovation #2: Adaptive Backpressure
//!
//! Replaces naive spin-wait with an intelligent three-tier strategy:
//! 1. **Spin** (`core::hint::spin_loop`) — lowest latency, highest CPU
//! 2. **Yield** (`std::thread::yield_now`) — medium latency, gives up time slice
//! 3. **Sleep** (`std::thread::sleep(1ns)`) — highest latency, minimal CPU
//!
//! # Latency Decomposition
//!
//! The [`AdaptiveBackpressure::submit_decomposed`] function separates:
//! - **`write_ns`**: The TRUE cost of the ring write (what we actually want to measure)
//! - **`wait_ns`**: Time spent in backpressure (the harness artifact)
//! - **`total_ns`**: `write_ns + wait_ns` (what naive benchmarks measure)
//!
//! This directly addresses the 160× discrepancy in the original OTAP benchmark:
//! documented 41 ns vs measured ~6,600 ns. The difference was almost entirely
//! `wait_ns` — time spinning on `SLOT_FREE`.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant};

// ---------------------------------------------------------------------------
// Tier
// ---------------------------------------------------------------------------

/// Which backpressure tier was used to resolve an operation.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub enum Tier {
    /// Spin with `core::hint::spin_loop()` — lowest latency, highest CPU.
    /// Used for retry counts 0..spin_limit.
    #[default]
    Spin,
    /// Yield with `std::thread::yield_now()` — medium latency.
    /// Used for retry counts spin_limit..(spin_limit + yield_limit).
    Yield,
    /// Sleep with `std::thread::sleep(Duration::from_nanos(n))` — minimal CPU.
    /// Used for retry counts beyond spin + yield limits.
    Sleep,
}

// ---------------------------------------------------------------------------
// AdaptiveConfig
// ---------------------------------------------------------------------------

/// Configuration for adaptive backpressure behavior.
#[derive(Clone, Debug)]
pub struct AdaptiveConfig {
    /// Maximum spin iterations before yielding. Default: 1000.
    pub spin_limit: usize,
    /// Maximum yield iterations before sleeping. Default: 100.
    pub yield_limit: usize,
    /// Nanoseconds to sleep once we enter the Sleep tier. Default: 1.
    pub sleep_nanos: u64,
}

impl Default for AdaptiveConfig {
    fn default() -> Self {
        AdaptiveConfig {
            spin_limit: 1000,
            yield_limit: 100,
            sleep_nanos: 1,
        }
    }
}

// ---------------------------------------------------------------------------
// DecomposedLatency
// ---------------------------------------------------------------------------

/// Result of a decomposed latency measurement.
///
/// Separates the TRUE write cost from the backpressure wait time,
/// exposing the harness artifact that caused the 160× benchmark error.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct DecomposedLatency {
    /// Nanoseconds spent actually writing the item (true ring write cost).
    /// This is the number that matters for ring buffer performance.
    pub write_ns: u64,
    /// Nanoseconds spent waiting/backpressuring (harness artifact).
    /// This is noise that should be separated out and reported separately.
    pub wait_ns: u64,
    /// Total nanoseconds (write_ns + wait_ns + any overhead).
    /// This is what a naive benchmark would measure.
    pub total_ns: u64,
    /// Which tier resolved the operation (the highest tier reached).
    pub resolved_tier: Tier,
}

// ---------------------------------------------------------------------------
// AdaptiveBackpressure
// ---------------------------------------------------------------------------

/// Adaptive backpressure controller with latency decomposition.
///
/// # Example
/// ```
/// use otap_bench::adaptive::{AdaptiveBackpressure, AdaptiveConfig, Tier};
///
/// let config = AdaptiveConfig::default();
/// let adaptive = AdaptiveBackpressure::new(config);
///
/// // First 1000 retries use Spin, next 100 use Yield, then Sleep
/// assert_eq!(adaptive.wait(0), Tier::Spin);
/// assert_eq!(adaptive.wait(999), Tier::Spin);
/// assert_eq!(adaptive.wait(1000), Tier::Yield);
/// ```
pub struct AdaptiveBackpressure {
    config: AdaptiveConfig,
    /// Cumulative retry count across all operations (diagnostic).
    retry_count: AtomicUsize,
}

impl AdaptiveBackpressure {
    /// Create a new adaptive backpressure controller.
    pub fn new(config: AdaptiveConfig) -> Self {
        AdaptiveBackpressure {
            config,
            retry_count: AtomicUsize::new(0),
        }
    }

    /// Wait adaptively based on retry count.
    ///
    /// Returns which tier was used. The retry count maps to tiers as:
    /// - `0..spin_limit` → [`Tier::Spin`]
    /// - `spin_limit..(spin_limit + yield_limit)` → [`Tier::Yield`]
    /// - `>= (spin_limit + yield_limit)` → [`Tier::Sleep`]
    pub fn wait(&self, retry: usize) -> Tier {
        self.retry_count.fetch_add(1, Ordering::Relaxed);

        if retry < self.config.spin_limit {
            for _ in 0..16 {
                core::hint::spin_loop();
            }
            Tier::Spin
        } else if retry < self.config.spin_limit + self.config.yield_limit {
            std::thread::yield_now();
            Tier::Yield
        } else {
            std::thread::sleep(Duration::from_nanos(self.config.sleep_nanos));
            Tier::Sleep
        }
    }

    /// Decompose a blocking submit: returns the latency broken into
    /// `write_ns` (true cost) and `wait_ns` (backpressure artifact).
    ///
    /// The `try_op` closure should attempt the operation. If it succeeds,
    /// the closure should return `true`. If it fails (ring full, etc.),
    /// return `false` and adaptive wait will be applied before retrying.
    ///
    /// # Example
    /// ```
    /// use otap_bench::adaptive::{AdaptiveBackpressure, AdaptiveConfig};
    ///
    /// let adaptive = AdaptiveBackpressure::new(AdaptiveConfig::default());
    /// let result = adaptive.submit_decomposed(
    ///     |_item: u64| true,  // succeeds immediately
    ///     42u64,
    /// );
    /// assert_eq!(result.wait_ns, 0); // no waiting needed
    /// ```
    pub fn submit_decomposed<F, T>(&self, mut try_op: F, item: T) -> DecomposedLatency
    where
        F: FnMut(T) -> bool,
        T: Copy,
    {
        let total_start = Instant::now();
        let mut wait_ns: u64 = 0;
        let mut retry: usize = 0;
        let mut resolved_tier = Tier::Spin;

        loop {
            let attempt_start = Instant::now();
            if try_op(item) {
                // Success! Measure the actual write time.
                let write_ns = attempt_start.elapsed().as_nanos() as u64;
                let total_ns = total_start.elapsed().as_nanos() as u64;

                return DecomposedLatency {
                    write_ns,
                    wait_ns,
                    total_ns,
                    resolved_tier,
                };
            }

            // Failed — measure the wait time separately
            let wait_start = Instant::now();
            resolved_tier = self.wait(retry);
            wait_ns += wait_start.elapsed().as_nanos() as u64;
            retry += 1;
        }
    }

    /// Decompose a blocking drain: returns the latency broken into
    /// `write_ns` (true cost of drain operation) and `wait_ns` (backpressure).
    ///
    /// The `try_op` closure should attempt to drain. If it succeeds, return `Some(T)`.
    /// If the ring is empty, return `None` and adaptive wait will be applied.
    pub fn drain_decomposed<F, T>(&self, mut try_op: F) -> (Option<T>, DecomposedLatency)
    where
        F: FnMut() -> Option<T>,
    {
        let total_start = Instant::now();
        let mut wait_ns: u64 = 0;
        let mut retry: usize = 0;
        let mut resolved_tier = Tier::Spin;

        loop {
            let attempt_start = Instant::now();
            if let Some(item) = try_op() {
                // Success! Measure the actual drain time.
                let write_ns = attempt_start.elapsed().as_nanos() as u64;
                let total_ns = total_start.elapsed().as_nanos() as u64;

                return (
                    Some(item),
                    DecomposedLatency {
                        write_ns,
                        wait_ns,
                        total_ns,
                        resolved_tier,
                    },
                );
            }

            // Failed — measure the wait time separately
            let wait_start = Instant::now();
            resolved_tier = self.wait(retry);
            wait_ns += wait_start.elapsed().as_nanos() as u64;
            retry += 1;
        }
    }

    /// Get the current cumulative retry count (diagnostic).
    pub fn retry_count(&self) -> usize {
        self.retry_count.load(Ordering::Relaxed)
    }

    /// Reset the retry counter to zero.
    pub fn reset(&self) {
        self.retry_count.store(0, Ordering::Relaxed);
    }
}

// Safety: AdaptiveBackpressure uses only atomic operations internally
unsafe impl Send for AdaptiveBackpressure {}
unsafe impl Sync for AdaptiveBackpressure {}
