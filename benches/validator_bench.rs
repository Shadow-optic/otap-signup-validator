use criterion::{black_box, criterion_group, criterion_main, Criterion};
use terabit_fabric::validator::SignupData;

fn bench_signup_validate(c: &mut Criterion) {
    let good = SignupData {
        username: "operator-chi".to_string(),
        password: "p@$$w0rD123".to_string(),
        confirm_password: "p@$$w0rD123".to_string(),
    };
    let bad = SignupData {
        username: "operator-chi".to_string(),
        password: "p@$$w0rD123".to_string(),
        confirm_password: "different".to_string(),
    };

    c.bench_function("signup_validate_ok", |b| {
        b.iter(|| black_box(&good).validate_signup().is_ok())
    });
    c.bench_function("signup_validate_err", |b| {
        b.iter(|| black_box(&bad).validate_signup().is_err())
    });
}

criterion_group!(benches, bench_signup_validate);
criterion_main!(benches);
