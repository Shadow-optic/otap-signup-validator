//! D3 crypto performance benchmarks.

use criterion::{black_box, criterion_group, criterion_main, Criterion};
use otap_d3::{
    calculate_rpc, Initiator, Polarization, Responder, RpcDomain, Symbol,
};
use ed25519_dalek::SigningKey;
use rand::rngs::OsRng;

fn bench_rpc(c: &mut Criterion) {
    let symbols: Vec<Symbol> = (0..16)
        .map(|i| Symbol {
            value: i as u16,
            lambda: (i as u8) & 0x3F,
            pol: Polarization::H,
        })
        .collect();
    c.bench_function("rpc_16_symbols", |b| {
        b.iter(|| calculate_rpc(black_box(&symbols), black_box(0), black_box(0xDEAD_BEEF)));
    });
}

fn bench_seal_open(c: &mut Criterion) {
    let alice_sk = SigningKey::generate(&mut OsRng);
    let bob_sk = SigningKey::generate(&mut OsRng);

    let (init_wait, m1) = Initiator::new(alice_sk.clone(), bob_sk.verifying_key()).start();
    let (resp_wait, m2) = Responder::new(bob_sk.clone(), alice_sk.verifying_key())
        .receive_msg1(m1);
    let (alice, m3) = init_wait.receive_msg2(m2).unwrap();
    let bob = resp_wait.receive_msg3(m3).unwrap();

    let payload: Vec<Symbol> = (0..8)
        .map(|i| Symbol {
            value: i as u16,
            lambda: 1,
            pol: Polarization::H,
        })
        .collect();

    c.bench_function("seal", |b| {
        b.iter(|| {
            let frame = alice
                .seal(RpcDomain::LinkData, black_box(payload.clone()))
                .unwrap();
            black_box(frame);
        });
    });

    let frame = alice.seal(RpcDomain::LinkData, payload.clone()).unwrap();
    c.bench_function("open", |b| {
        b.iter(|| {
            let opened = bob.open(RpcDomain::LinkData, black_box(&frame)).unwrap();
            black_box(opened);
        });
    });
}

criterion_group!(benches, bench_rpc, bench_seal_open);
criterion_main!(benches);
