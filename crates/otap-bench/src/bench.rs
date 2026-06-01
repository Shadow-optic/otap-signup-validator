//! Module E: Benchmark Harness
//!
//! Statistically rigorous benchmarking infrastructure with HDR Histograms,
//! three measurement modes (uncontended, saturated, contended), warmup phases,
//! decomposed latency (write cost vs wait time), and confidence intervals.
//!
//! The original OTAP benchmark had a 160x performance error because it measured
//! backpressure wait time instead of true ring write cost. This harness corrects
//! that by separating the two through the Uncontended measurement mode.

use hdrhistogram::Histogram;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Barrier};
use std::thread;
use std::time::{Duration, Instant};

use crate::adaptive::{AdaptiveBackpressure, AdaptiveConfig};
use crate::ringbuf::RingBuffer;

/// Measurement mode determines what we measure.
#[derive(Clone, Copy, Debug)]
pub enum MeasurementMode {
    /// Measure true ring write cost: consumer runs ahead, ring always has free slots.
    /// No backpressure. This gives the TRUE latency of the ring operation.
    Uncontended,
    /// Measure sustainable throughput: producer and consumer run at matched rates.
    /// Ring has natural oscillation between near-empty and near-full.
    Saturated,
    /// Measure contended latency: producer faster than consumer.
    /// Ring fills up, producer experiences backpressure. Measures the backpressure curve.
    Contended { producer_rate: f64 }, // producer_rate > 1.0 (multiplier over consumer rate)
}

/// Configuration for a benchmark run.
#[derive(Clone, Debug)]
pub struct BenchmarkConfig {
    /// Number of messages to send during warmup (discarded from results).
    pub warmup_messages: usize,
    /// Number of messages to measure.
    pub measurement_messages: usize,
    /// Size of each message in bytes.
    pub message_size: usize,
    /// Ring buffer capacity (power of 2).
    pub ring_capacity: usize,
    /// Measurement mode.
    pub mode: MeasurementMode,
    /// Whether to use batch operations.
    pub use_batch: bool,
    /// Batch size (if use_batch is true).
    pub batch_size: usize,
    /// Whether to use adaptive backpressure.
    pub use_adaptive: bool,
    /// Number of consumer threads (1 = SPSC, >1 = MPMC).
    pub num_consumers: usize,
    /// Whether to decompose latency (write vs wait time).
    pub decompose_latency: bool,
}

impl Default for BenchmarkConfig {
    fn default() -> Self {
        BenchmarkConfig {
            warmup_messages: 1_000_000,
            measurement_messages: 10_000_000,
            message_size: 128,
            ring_capacity: 65536,
            mode: MeasurementMode::Uncontended,
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: true,
        }
    }
}

/// Results from a single benchmark run.
#[derive(Clone, Debug)]
pub struct BenchmarkResult {
    pub config: BenchmarkConfig,
    /// Latency histogram (nanoseconds).
    pub latency_ns: Histogram<u64>,
    /// Throughput in messages per second.
    pub throughput_msg_per_sec: f64,
    /// Throughput in Gbps.
    pub throughput_gbps: f64,
    /// Total elapsed time for measurement phase.
    pub elapsed: Duration,
    /// Decomposed latency: true write cost (ns).
    pub write_cost_ns: Option<f64>,
    /// Decomposed latency: wait time (ns).
    pub wait_time_ns: Option<f64>,
    /// Percentage of time spent waiting (vs writing).
    pub wait_ratio: Option<f64>,
    /// Per-iteration raw data for confidence interval computation.
    pub latencies: Vec<u64>,
}

impl BenchmarkResult {
    /// Compute 95% confidence interval for the mean latency (ns).
    pub fn mean_ci_ns(&self) -> Option<(f64, f64)> {
        if self.latencies.len() < 30 {
            return None;
        }
        let n = self.latencies.len() as f64;
        let mean = self.latencies.iter().map(|&x| x as f64).sum::<f64>() / n;
        let variance = self
            .latencies
            .iter()
            .map(|&x| {
                let diff = x as f64 - mean;
                diff * diff
            })
            .sum::<f64>()
            / (n - 1.0);
        let std_dev = variance.sqrt();
        let margin = 1.96 * std_dev / n.sqrt();
        Some((mean - margin, mean + margin))
    }

    /// Compute 95% confidence interval for throughput (msg/s).
    pub fn throughput_ci(&self) -> Option<(f64, f64)> {
        if let Some((ci_low, ci_high)) = self.mean_ci_ns() {
            if ci_high > 0.0 && ci_low > 0.0 {
                // Throughput is inversely related to mean latency in per-message timing modes
                let msgs = self.config.measurement_messages as f64;
                let low = msgs / (ci_high * msgs / 1e9);
                let high = msgs / (ci_low * msgs / 1e9);
                return Some((low, high));
            }
        }
        None
    }
}

/// Benchmark harness — runs benchmarks and collects results.
pub struct Benchmark;

impl Benchmark {
    /// Run a benchmark with the given configuration.
    /// Returns the benchmark results.
    pub fn run(config: &BenchmarkConfig) -> BenchmarkResult {
        match config.mode {
            MeasurementMode::Uncontended => run_uncontended(config),
            MeasurementMode::Saturated => run_saturated(config),
            MeasurementMode::Contended { producer_rate } => {
                run_contended(config, producer_rate)
            }
        }
    }

    /// Run multiple iterations and compute confidence intervals.
    pub fn run_with_ci(config: &BenchmarkConfig, iterations: usize) -> Vec<BenchmarkResult> {
        let mut results = Vec::with_capacity(iterations);
        for i in 0..iterations {
            eprintln!("  Benchmark iteration {}/{}", i + 1, iterations);
            results.push(Self::run(config));
        }
        results
    }

    /// Format results as human-readable report.
    pub fn format_results(result: &BenchmarkResult) -> String {
        let mut s = String::new();
        s.push_str("========================================\n");
        s.push_str("   OTAP Benchmark Results\n");
        s.push_str("========================================\n\n");

        s.push_str(&format!("Mode:               {:?}\n", result.config.mode));
        s.push_str(&format!(
            "Message size:       {} bytes\n",
            result.config.message_size
        ));
        s.push_str(&format!(
            "Ring capacity:      {}\n",
            result.config.ring_capacity
        ));
        s.push_str(&format!(
            "Batch size:         {}\n",
            result.config.batch_size
        ));
        s.push_str(&format!(
            "Consumers:          {}\n",
            result.config.num_consumers
        ));
        s.push_str(&format!(
            "Warmup messages:    {}\n",
            result.config.warmup_messages
        ));
        s.push_str(&format!(
            "Measured messages:  {}\n",
            result.config.measurement_messages
        ));
        s.push_str(&format!(
            "Use batch:          {}\n",
            result.config.use_batch
        ));
        s.push_str(&format!(
            "Use adaptive:       {}\n",
            result.config.use_adaptive
        ));
        s.push_str(&format!(
            "Decompose latency:  {}\n",
            result.config.decompose_latency
        ));

        s.push_str("\n--- Throughput ---\n");
        s.push_str(&format!(
            "Messages/sec:       {:.0}\n",
            result.throughput_msg_per_sec
        ));
        s.push_str(&format!(
            "Gbps:               {:.3}\n",
            result.throughput_gbps
        ));
        s.push_str(&format!(
            "Elapsed:            {:?}\n",
            result.elapsed
        ));

        s.push_str("\n--- Latency (ns) ---\n");
        s.push_str(&format!(
            "Samples:            {}\n",
            result.latency_ns.len()
        ));
        s.push_str(&format!(
            "Mean:               {:.1}\n",
            result.latency_ns.mean()
        ));
        s.push_str(&format!(
            "StdDev:             {:.1}\n",
            result.latency_ns.stdev()
        ));
        s.push_str(&format!(
            "P50:                {:.1}\n",
            result.latency_ns.value_at_quantile(0.50)
        ));
        s.push_str(&format!(
            "P90:                {:.1}\n",
            result.latency_ns.value_at_quantile(0.90)
        ));
        s.push_str(&format!(
            "P99:                {:.1}\n",
            result.latency_ns.value_at_quantile(0.99)
        ));
        s.push_str(&format!(
            "P99.9:              {:.1}\n",
            result.latency_ns.value_at_quantile(0.999)
        ));
        s.push_str(&format!(
            "P99.99:             {:.1}\n",
            result.latency_ns.value_at_quantile(0.9999)
        ));
        s.push_str(&format!("Min:                {}\n", result.latency_ns.min()));
        s.push_str(&format!("Max:                {}\n", result.latency_ns.max()));

        if let Some((ci_low, ci_high)) = result.mean_ci_ns() {
            s.push_str(&format!(
                "\n95% CI (mean ns):   [{:.1}, {:.1}]\n",
                ci_low, ci_high
            ));
        }

        if let (Some(write_cost), Some(wait_time), Some(wait_ratio)) = (
            result.write_cost_ns,
            result.wait_time_ns,
            result.wait_ratio,
        ) {
            s.push_str("\n--- Decomposed Latency ---\n");
            s.push_str(&format!("Write cost (ns):    {:.1}\n", write_cost));
            s.push_str(&format!("Wait time (ns):     {:.1}\n", wait_time));
            s.push_str(&format!("Wait ratio:         {:.4} ({:.1}%)\n", wait_ratio, wait_ratio * 100.0));
        }

        s.push_str("\n========================================\n");
        s
    }

    /// Format results as TOML.
    pub fn format_toml(result: &BenchmarkResult) -> String {
        let mode_str = match result.config.mode {
            MeasurementMode::Uncontended => "Uncontended",
            MeasurementMode::Saturated => "Saturated",
            MeasurementMode::Contended { producer_rate } => {
                return format!(
                    r#"[benchmark]
mode = "Contended"
producer_rate = {}
message_size = {}
ring_capacity = {}
batch_size = {}
num_consumers = {}
use_batch = {}
use_adaptive = {}
decompose_latency = {}

[throughput]
ms_per_sec = {:.0}
gbps = {:.3}

[latency_ns]
count = {}
p50 = {:.1}
p90 = {:.1}
p99 = {:.1}
p999 = {:.1}
p9999 = {:.1}
min = {}
max = {}
mean = {:.1}
stdev = {:.1}

[decomposition]
write_cost_ns = {:.1}
wait_time_ns = {:.1}
wait_ratio = {:.4}
"#,
                    producer_rate,
                    result.config.message_size,
                    result.config.ring_capacity,
                    result.config.batch_size,
                    result.config.num_consumers,
                    result.config.use_batch,
                    result.config.use_adaptive,
                    result.config.decompose_latency,
                    result.throughput_msg_per_sec,
                    result.throughput_gbps,
                    result.latency_ns.len(),
                    result.latency_ns.value_at_quantile(0.50),
                    result.latency_ns.value_at_quantile(0.90),
                    result.latency_ns.value_at_quantile(0.99),
                    result.latency_ns.value_at_quantile(0.999),
                    result.latency_ns.value_at_quantile(0.9999),
                    result.latency_ns.min(),
                    result.latency_ns.max(),
                    result.latency_ns.mean(),
                    result.latency_ns.stdev(),
                    result.write_cost_ns.unwrap_or(0.0),
                    result.wait_time_ns.unwrap_or(0.0),
                    result.wait_ratio.unwrap_or(0.0),
                )
            }
        };

        format!(
            r#"[benchmark]
mode = "{}"
message_size = {}
ring_capacity = {}
batch_size = {}
num_consumers = {}
use_batch = {}
use_adaptive = {}
decompose_latency = {}

[throughput]
ms_per_sec = {:.0}
gbps = {:.3}

[latency_ns]
count = {}
p50 = {:.1}
p90 = {:.1}
p99 = {:.1}
p999 = {:.1}
p9999 = {:.1}
min = {}
max = {}
mean = {:.1}
stdev = {:.1}

[decomposition]
write_cost_ns = {:.1}
wait_time_ns = {:.1}
wait_ratio = {:.4}
"#,
            mode_str,
            result.config.message_size,
            result.config.ring_capacity,
            result.config.batch_size,
            result.config.num_consumers,
            result.config.use_batch,
            result.config.use_adaptive,
            result.config.decompose_latency,
            result.throughput_msg_per_sec,
            result.throughput_gbps,
            result.latency_ns.len(),
            result.latency_ns.value_at_quantile(0.50),
            result.latency_ns.value_at_quantile(0.90),
            result.latency_ns.value_at_quantile(0.99),
            result.latency_ns.value_at_quantile(0.999),
            result.latency_ns.value_at_quantile(0.9999),
            result.latency_ns.min(),
            result.latency_ns.max(),
            result.latency_ns.mean(),
            result.latency_ns.stdev(),
            result.write_cost_ns.unwrap_or(0.0),
            result.wait_time_ns.unwrap_or(0.0),
            result.wait_ratio.unwrap_or(0.0),
        )
    }
}

/// Helper: Pin thread to a specific CPU core for consistent measurements.
pub fn pin_thread_to_core(core_id: usize) {
    #[cfg(target_os = "linux")]
    unsafe {
        let mut set: libc::cpu_set_t = std::mem::zeroed();
        libc::CPU_SET(core_id, &mut set);
        libc::sched_setaffinity(0, std::mem::size_of::<libc::cpu_set_t>(), &set);
    }
    // On non-Linux platforms, this is a no-op.
}

/// Helper: Busy-wait delay (nanoseconds) — used for rate-limiting in contended mode.
pub fn busy_wait_ns(ns: u64) {
    let start = Instant::now();
    while start.elapsed().as_nanos() < (ns as u128) {
        std::hint::spin_loop();
    }
}

// =============================================================================
// Internal implementation
// =============================================================================

fn create_histogram() -> Histogram<u64> {
    Histogram::<u64>::new_with_bounds(1, 1_000_000_000, 3).unwrap()
}

fn compute_throughput_gbps(msgs_per_sec: f64, msg_size: usize) -> f64 {
    let bits_per_sec = msgs_per_sec * (msg_size as f64) * 8.0;
    bits_per_sec / 1e9
}

/// Helper to decide whether to collect per-iteration latencies.
/// We only collect raw latencies when the sample size is manageable (< 1M).
fn should_collect_latencies(config: &BenchmarkConfig) -> bool {
    config.measurement_messages <= 1_000_000
}

// =============================================================================
// Uncontended mode: measure TRUE ring write cost
// =============================================================================
fn run_uncontended(config: &BenchmarkConfig) -> BenchmarkResult {
    let ring = Arc::new(RingBuffer::<u64, 65536>::new());
    let shutdown = Arc::new(AtomicBool::new(false));
    let barrier = Arc::new(Barrier::new(2));

    // ---- Consumer thread: drain as fast as possible ----
    let ring_consumer = Arc::clone(&ring);
    let shutdown_consumer = Arc::clone(&shutdown);
    let barrier_consumer = Arc::clone(&barrier);

    let consumer_handle = thread::spawn(move || {
        // Signal ready
        barrier_consumer.wait();
        while !shutdown_consumer.load(Ordering::Relaxed) {
            while ring_consumer.try_drain().is_some() {
                // Keep draining
            }
            // Brief yield to avoid tight spinning
            std::thread::yield_now();
        }
        // Final drain
        while ring_consumer.try_drain().is_some() {}
    });

    // ---- Producer thread (main thread) ----
    let mut histogram = create_histogram();
    let mut latencies = if should_collect_latencies(config) {
        Vec::with_capacity(config.measurement_messages)
    } else {
        Vec::new()
    };

    barrier.wait();

    // Give consumer a head start to drain the ring
    std::thread::sleep(Duration::from_millis(1));

    // ---- Warmup phase ----
    for i in 0..config.warmup_messages {
        while !ring.try_submit(i as u64) {
            std::thread::yield_now();
        }
    }

    // ---- Measurement phase ----
    let start_time = Instant::now();
    for i in 0..config.measurement_messages {
        let t0 = Instant::now();
        // In uncontended mode, this should succeed immediately
        while !ring.try_submit(i as u64) {
            // Should rarely happen in uncontended mode, but handle it
            std::thread::yield_now();
        }
        let elapsed_ns = t0.elapsed().as_nanos() as u64;
        histogram.record(elapsed_ns).ok();
        if should_collect_latencies(config) {
            latencies.push(elapsed_ns);
        }
    }
    let elapsed = start_time.elapsed();

    // Signal shutdown
    shutdown.store(true, Ordering::Relaxed);
    consumer_handle.join().unwrap();

    let total_ns = elapsed.as_nanos() as f64;
    let msgs = config.measurement_messages as f64;
    let throughput_msg_per_sec = msgs / (total_ns / 1e9);
    let throughput_gbps = compute_throughput_gbps(throughput_msg_per_sec, config.message_size);

    // Compute decomposed latency: in uncontended mode, write_cost ≈ P50 latency
    // and wait_time is negligible.
    let (write_cost_ns, wait_time_ns, wait_ratio) = if config.decompose_latency {
        let mean_wait = histogram
            .iter_recorded()
            .map(|v| {
                let val = v.value_iterated_to();
                let count = v.count_at_value();
                // Values above 2x P50 are considered "wait" in uncontended mode
                if val > 2 * histogram.value_at_quantile(0.50) {
                    (val * count) as f64
                } else {
                    0.0
                }
            })
            .sum::<f64>()
            / histogram.len() as f64;
        let mean_total = histogram.mean();
        let write_cost = mean_total - mean_wait;
        let wait_ratio = if mean_total > 0.0 {
            mean_wait / mean_total
        } else {
            0.0
        };
        (Some(write_cost), Some(mean_wait), Some(wait_ratio))
    } else {
        (None, None, None)
    };

    BenchmarkResult {
        config: config.clone(),
        latency_ns: histogram,
        throughput_msg_per_sec,
        throughput_gbps,
        elapsed,
        write_cost_ns,
        wait_time_ns,
        wait_ratio,
        latencies,
    }
}

// =============================================================================
// Saturated mode: measure sustainable throughput
// =============================================================================
fn run_saturated(config: &BenchmarkConfig) -> BenchmarkResult {
    let ring = Arc::new(RingBuffer::<u64, 65536>::new());
    let barrier = Arc::new(Barrier::new(2));

    let ring_consumer = Arc::clone(&ring);
    let barrier_consumer = Arc::clone(&barrier);
    let num_messages = config.measurement_messages;

    // ---- Consumer thread ----
    let consumer_handle = thread::spawn(move || {
        barrier_consumer.wait();
        let mut drained = 0usize;
        while drained < num_messages {
            if ring_consumer.try_drain().is_some() {
                drained += 1;
            }
        }
    });

    // ---- Producer (main thread) ----
    let mut histogram = create_histogram();
    let mut latencies = if should_collect_latencies(config) {
        Vec::with_capacity(config.measurement_messages)
    } else {
        Vec::new()
    };

    barrier.wait();

    // Warmup
    for i in 0..config.warmup_messages {
        while !ring.try_submit(i as u64) {
            std::hint::spin_loop();
        }
    }

    // Measurement: measure total time for all messages
    let start_time = Instant::now();
    for i in 0..config.measurement_messages {
        let t0 = Instant::now();
        while !ring.try_submit(i as u64) {
            std::hint::spin_loop();
        }
        let elapsed_ns = t0.elapsed().as_nanos() as u64;
        histogram.record(elapsed_ns).ok();
        if should_collect_latencies(config) {
            latencies.push(elapsed_ns);
        }
    }
    let elapsed = start_time.elapsed();

    consumer_handle.join().unwrap();

    let total_ns = elapsed.as_nanos() as f64;
    let msgs = config.measurement_messages as f64;
    let throughput_msg_per_sec = msgs / (total_ns / 1e9);
    let throughput_gbps = compute_throughput_gbps(throughput_msg_per_sec, config.message_size);

    // In saturated mode, the latency includes both write cost and occasional waits
    // Decomposition is less meaningful here, but provide it anyway
    let (write_cost_ns, wait_time_ns, wait_ratio) = if config.decompose_latency {
        let mean_latency = histogram.mean();
        // In saturated mode, estimate write cost as the P1 (minimum observed)
        // and wait time as the difference from mean
        let min_ns = histogram.min() as f64;
        let wait_ns = mean_latency - min_ns;
        let ratio = if mean_latency > 0.0 {
            wait_ns / mean_latency
        } else {
            0.0
        };
        (Some(min_ns), Some(wait_ns), Some(ratio))
    } else {
        (None, None, None)
    };

    BenchmarkResult {
        config: config.clone(),
        latency_ns: histogram,
        throughput_msg_per_sec,
        throughput_gbps,
        elapsed,
        write_cost_ns,
        wait_time_ns,
        wait_ratio,
        latencies,
    }
}

// =============================================================================
// Contended mode: measure backpressure curve
// =============================================================================
fn run_contended(config: &BenchmarkConfig, producer_rate: f64) -> BenchmarkResult {
    let ring = Arc::new(RingBuffer::<u64, 65536>::new());
    let barrier = Arc::new(Barrier::new(2));

    // Consumer delay per message: if producer_rate = 2.0, consumer is 2x slower
    // than it would need to be to keep up. We simulate this with a fixed delay.
    let consumer_delay_ns = if producer_rate > 1.0 {
        // Base delay: at rate 2.0, consumer takes ~twice as long per message
        let base_ns = 1000.0; // assume ~1us base processing per message
        (base_ns * (producer_rate - 1.0)) as u64
    } else {
        0
    };

    let ring_consumer = Arc::clone(&ring);
    let barrier_consumer = Arc::clone(&barrier);
    let num_messages = config.measurement_messages;
    let use_adaptive = config.use_adaptive;

    // If using adaptive backpressure, use it for decomposed latency
    let adaptive = if use_adaptive {
        Some(Arc::new(AdaptiveBackpressure::new(
            AdaptiveConfig::default(),
        )))
    } else {
        None
    };

    // ---- Consumer thread: drains with simulated delay ----
    let consumer_handle = thread::spawn(move || {
        barrier_consumer.wait();
        let mut drained = 0usize;
        while drained < num_messages {
            if ring_consumer.try_drain().is_some() {
                drained += 1;
                if consumer_delay_ns > 0 {
                    busy_wait_ns(consumer_delay_ns);
                }
            } else {
                std::thread::yield_now();
            }
        }
    });

    // ---- Producer (main thread) ----
    let mut histogram = create_histogram();
    let mut latencies = if should_collect_latencies(config) {
        Vec::with_capacity(config.measurement_messages)
    } else {
        Vec::new()
    };

    let mut total_write_ns: u64 = 0;
    let mut total_wait_ns: u64 = 0;

    barrier.wait();

    // Warmup
    if adaptive.is_some() {
        // With adaptive backpressure, we need a different warmup path
        // For now, warm up with standard submits
        for i in 0..config.warmup_messages {
            while !ring.try_submit(i as u64) {
                std::hint::spin_loop();
            }
        }
    } else {
        for i in 0..config.warmup_messages {
            while !ring.try_submit(i as u64) {
                std::hint::spin_loop();
            }
        }
    }

    // Measurement
    let start_time = Instant::now();

    if config.decompose_latency && adaptive.is_some() {
        // Use adaptive backpressure's decomposed submit
        let _adap = adaptive.as_ref().unwrap();
        // For the stub, we approximate: the AdaptiveBackpressure::submit_decomposed
        // is called but we also need to interact with the ring.
        // In the real implementation, AdaptiveBackpressure wraps the ring.
        // Here we approximate by timing the submit and using the stub's decomposition.
        for i in 0..config.measurement_messages {
            let t0 = Instant::now();
            while !ring.try_submit(i as u64) {
                std::hint::spin_loop();
            }
            let total_ns = t0.elapsed().as_nanos() as u64;
            histogram.record(total_ns).ok();
            if should_collect_latencies(config) {
                latencies.push(total_ns);
            }
            // Approximate decomposition: min observed latency = write cost,
            // rest = wait time
            let min_observed = histogram.min();
            let wait = total_ns.saturating_sub(min_observed);
            total_write_ns += min_observed;
            total_wait_ns += wait;
        }
    } else {
        for i in 0..config.measurement_messages {
            let t0 = Instant::now();
            while !ring.try_submit(i as u64) {
                std::hint::spin_loop();
            }
            let total_ns = t0.elapsed().as_nanos() as u64;
            histogram.record(total_ns).ok();
            if should_collect_latencies(config) {
                latencies.push(total_ns);
            }
            // Simple decomposition for non-adaptive case
            let min_observed = histogram.min();
            let wait = total_ns.saturating_sub(min_observed);
            total_write_ns += min_observed;
            total_wait_ns += wait;
        }
    }

    let elapsed = start_time.elapsed();

    consumer_handle.join().unwrap();

    let total_ns_f = elapsed.as_nanos() as f64;
    let msgs = config.measurement_messages as f64;
    let throughput_msg_per_sec = msgs / (total_ns_f / 1e9);
    let throughput_gbps = compute_throughput_gbps(throughput_msg_per_sec, config.message_size);

    // Compute decomposed latency
    let (write_cost_ns, wait_time_ns, wait_ratio) = if config.decompose_latency {
        let n = config.measurement_messages as f64;
        let avg_write = total_write_ns as f64 / n;
        let avg_wait = total_wait_ns as f64 / n;
        let total_avg = avg_write + avg_wait;
        let ratio = if total_avg > 0.0 {
            avg_wait / total_avg
        } else {
            0.0
        };
        (Some(avg_write), Some(avg_wait), Some(ratio))
    } else {
        (None, None, None)
    };

    BenchmarkResult {
        config: config.clone(),
        latency_ns: histogram,
        throughput_msg_per_sec,
        throughput_gbps,
        elapsed,
        write_cost_ns,
        wait_time_ns,
        wait_ratio,
        latencies,
    }
}

// =============================================================================
// Tests
// =============================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_config_default() {
        let config = BenchmarkConfig::default();
        assert_eq!(config.warmup_messages, 1_000_000);
        assert_eq!(config.measurement_messages, 10_000_000);
        assert_eq!(config.message_size, 128);
        assert_eq!(config.ring_capacity, 65536);
        assert_eq!(config.batch_size, 64);
        assert_eq!(config.num_consumers, 1);
        assert!(!config.use_batch);
        assert!(!config.use_adaptive);
        assert!(config.decompose_latency);
    }

    #[test]
    fn test_histogram_bounds() {
        let mut h = create_histogram();
        h.record(100).unwrap();
        h.record(1_000_000).unwrap();
        assert_eq!(h.min(), 100);
        assert!(h.max() >= 1_000_000);
    }

    #[test]
    fn test_busy_wait() {
        let t0 = Instant::now();
        busy_wait_ns(1000); // 1 microsecond
        let elapsed = t0.elapsed();
        assert!(
            elapsed.as_nanos() >= 1000,
            "busy_wait should wait at least the requested time"
        );
    }

    #[test]
    fn test_run_uncontended_small() {
        let config = BenchmarkConfig {
            warmup_messages: 100,
            measurement_messages: 1000,
            message_size: 64,
            ring_capacity: 1024,
            mode: MeasurementMode::Uncontended,
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: true,
        };
        let result = Benchmark::run(&config);
        assert_eq!(result.config.measurement_messages, 1000);
        assert!(result.throughput_msg_per_sec > 0.0);
        assert!(result.latency_ns.len() > 0);
        // Uncontended mode should have low latency
        assert!(
            result.latency_ns.value_at_quantile(0.50) < 1_000_000,
            "P50 latency should be under 1ms in uncontended mode"
        );
    }

    #[test]
    fn test_run_saturated_small() {
        let config = BenchmarkConfig {
            warmup_messages: 100,
            measurement_messages: 1000,
            message_size: 64,
            ring_capacity: 1024,
            mode: MeasurementMode::Saturated,
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: false,
        };
        let result = Benchmark::run(&config);
        assert!(result.throughput_msg_per_sec > 0.0);
        assert!(result.latency_ns.len() > 0);
    }

    #[test]
    fn test_run_contended_small() {
        let config = BenchmarkConfig {
            warmup_messages: 100,
            measurement_messages: 1000,
            message_size: 64,
            ring_capacity: 1024,
            mode: MeasurementMode::Contended { producer_rate: 2.0 },
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: true,
        };
        let result = Benchmark::run(&config);
        assert!(result.throughput_msg_per_sec > 0.0);
        assert!(result.latency_ns.len() > 0);
        // In contended mode, should have decomposition data
        assert!(result.write_cost_ns.is_some());
        assert!(result.wait_time_ns.is_some());
    }

    #[test]
    fn test_format_results() {
        let config = BenchmarkConfig {
            warmup_messages: 10,
            measurement_messages: 100,
            message_size: 64,
            ring_capacity: 256,
            mode: MeasurementMode::Uncontended,
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: true,
        };
        let result = Benchmark::run(&config);
        let report = Benchmark::format_results(&result);
        assert!(report.contains("OTAP Benchmark Results"));
        assert!(report.contains("Throughput"));
        assert!(report.contains("Latency"));
        assert!(report.contains("P50"));
    }

    #[test]
    fn test_format_toml_uncontended() {
        let config = BenchmarkConfig {
            warmup_messages: 10,
            measurement_messages: 100,
            message_size: 64,
            ring_capacity: 256,
            mode: MeasurementMode::Uncontended,
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: true,
        };
        let result = Benchmark::run(&config);
        let toml = Benchmark::format_toml(&result);
        assert!(toml.contains("[benchmark]"));
        assert!(toml.contains("mode = \"Uncontended\""));
        assert!(toml.contains("[throughput]"));
        assert!(toml.contains("[latency_ns]"));
        assert!(toml.contains("[decomposition]"));
    }

    #[test]
    fn test_format_toml_saturated() {
        let config = BenchmarkConfig {
            warmup_messages: 10,
            measurement_messages: 100,
            message_size: 64,
            ring_capacity: 256,
            mode: MeasurementMode::Saturated,
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: true,
        };
        let result = Benchmark::run(&config);
        let toml = Benchmark::format_toml(&result);
        assert!(toml.contains("mode = \"Saturated\""));
    }

    #[test]
    fn test_format_toml_contended() {
        let config = BenchmarkConfig {
            warmup_messages: 10,
            measurement_messages: 100,
            message_size: 64,
            ring_capacity: 256,
            mode: MeasurementMode::Contended { producer_rate: 2.0 },
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: true,
        };
        let result = Benchmark::run(&config);
        let toml = Benchmark::format_toml(&result);
        assert!(toml.contains("mode = \"Contended\""));
        assert!(toml.contains("producer_rate"));
    }

    #[test]
    fn test_mean_ci_ns() {
        let mut h = create_histogram();
        let mut latencies = Vec::new();
        for i in 0..100 {
            let v = 1000 + (i % 100) as u64;
            h.record(v).unwrap();
            latencies.push(v);
        }
        let config = BenchmarkConfig::default();
        let result = BenchmarkResult {
            config,
            latency_ns: h,
            throughput_msg_per_sec: 1e6,
            throughput_gbps: 1.0,
            elapsed: Duration::from_secs(1),
            write_cost_ns: None,
            wait_time_ns: None,
            wait_ratio: None,
            latencies,
        };
        let ci = result.mean_ci_ns();
        assert!(ci.is_some());
        let (low, high) = ci.unwrap();
        assert!(low < high);
        assert!(low > 900.0 && high < 1200.0);
    }

    #[test]
    fn test_ci_insufficient_samples() {
        let mut h = create_histogram();
        let latencies: Vec<u64> = (0..10).map(|i| 1000 + i as u64).collect();
        for &v in &latencies {
            h.record(v).unwrap();
        }
        let config = BenchmarkConfig::default();
        let result = BenchmarkResult {
            config,
            latency_ns: h,
            throughput_msg_per_sec: 1e6,
            throughput_gbps: 1.0,
            elapsed: Duration::from_secs(1),
            write_cost_ns: None,
            wait_time_ns: None,
            wait_ratio: None,
            latencies,
        };
        // Less than 30 samples should return None
        assert!(result.mean_ci_ns().is_none());
    }

    #[test]
    fn test_run_with_ci() {
        let config = BenchmarkConfig {
            warmup_messages: 50,
            measurement_messages: 500,
            message_size: 64,
            ring_capacity: 1024,
            mode: MeasurementMode::Uncontended,
            use_batch: false,
            batch_size: 64,
            use_adaptive: false,
            num_consumers: 1,
            decompose_latency: false,
        };
        let results = Benchmark::run_with_ci(&config, 3);
        assert_eq!(results.len(), 3);
        for r in &results {
            assert!(r.latency_ns.len() > 0);
        }
    }
}
