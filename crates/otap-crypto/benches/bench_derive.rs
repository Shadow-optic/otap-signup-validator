use criterion::{black_box, criterion_group, criterion_main, Criterion};
use otap_core::{OamMode, Wavelength};
use otap_crypto::{derive_trajectory, SharedSecret, TrajectoryContext};

pub fn bench_derive(c: &mut Criterion) {
    let secret = SharedSecret::test_from_label("perf-test");
    let payload = vec![0u8; 1024];
    let ctx = TrajectoryContext {
        wavelength: Wavelength::new(40).unwrap(),
        oam: OamMode::new(1).unwrap(),
        sequence: 42,
        payload: &payload,
    };

    c.bench_function("derive_trajectory", |b| {
        b.iter(|| derive_trajectory(black_box(&secret), black_box(&ctx)))
    });
}

criterion_group!(benches, bench_derive);
criterion_main!(benches);
