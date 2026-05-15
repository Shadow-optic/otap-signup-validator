use criterion::{criterion_group, criterion_main, Criterion};
use validator::Validate;

#[derive(Debug, Validate)]
struct SignupData {
    username: String,
    password: String,
    #[validate(must_match(other = "password"))]
    confirm_password: String,
}

fn bench_signup_validate(c: &mut Criterion) {
    let signup = SignupData {
        username: "operator-chi".to_string(),
        password: "p@$$w0rD123".to_string(),
        confirm_password: "p@$$w0rD123".to_string(),
    };

    c.bench_function("signup_validate", |b| b.iter(|| signup.validate()));
}

criterion_group!(benches, bench_signup_validate);
criterion_main!(benches);
