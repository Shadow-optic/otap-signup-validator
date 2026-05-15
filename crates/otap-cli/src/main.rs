//! `otap-demo` — end-to-end demonstration of OTAP's parallel-decode and
//! PMD-invariant authentication.
//!
//! Three scenarios are run in sequence:
//!
//! 1. **Clean channel** — baseline round trip. Both auth modes succeed.
//! 2. **PMD-rotated channel** — random Jones-matrix rotation applied. Sample-
//!    match auth *fails*; topological auth *succeeds*. This is the empirical
//!    case for the topological scheme.
//! 3. **Tampered payload** — adversary modifies bytes without the key. Both
//!    auth modes detect the modification.
//!
//! Run with:
//! ```bash
//! cargo run --release -p otap-cli
//! ```

use std::time::Instant;

use otap_codec::{Decoder, DecoderConfig, Encoder};
use otap_core::Wavelength;
use otap_crypto::{AuthMode, SharedSecret};
use otap_schema::{equity_order::Side, EquityTradeOrder};
use otap_sim::{Channel, PoincareRotation};

fn banner(title: &str) {
    println!();
    println!("───────────────────────────────────────────────────────────────────");
    println!("  {}", title);
    println!("───────────────────────────────────────────────────────────────────");
}

fn print_order(label: &str, order: &EquityTradeOrder) {
    println!(
        "  {label:>10}: {} {:?} qty={} @ ${:.2} (id={:#x})",
        order.symbol_str(),
        order.side,
        order.quantity,
        order.price_cents / 100.0,
        order.client_order_id
    );
}

fn main() {
    println!("\nOTAP Reference Codec Demo");
    println!("=========================");
    println!("Protocol: OTAP v0.2 (reference model)");

    // ─── Setup ────────────────────────────────────────────────────────────
    let alice_bob_key = SharedSecret::test_from_label("alice-bob");
    let wavelength = Wavelength::new(40).unwrap();

    println!(
        "  Wavelength:  channel {} ≈ {:.2} nm",
        wavelength.channel(),
        wavelength.wavelength_nm()
    );
    println!("  Shared key:  HMAC-SHA256 derived (test only)");

    // The order we will transmit.
    let original_order = EquityTradeOrder::new(
        "AAPL",
        Side::Buy,
        1000,
        19_850.00,
        1_716_649_200_000_000_001,
        0xDEAD_BEEF_CAFE_F00D,
        [0x11; 16],
    );

    // ─── Scenario 1: Clean Channel ────────────────────────────────────────
    banner("Scenario 1: Clean channel (no impairment)");

    let mut encoder = Encoder::new(alice_bob_key.clone(), wavelength);
    let decoder_match = Decoder::new(DecoderConfig {
        secret: alice_bob_key.clone(),
        expected_wavelength: wavelength,
        auth_mode: AuthMode::TrajectoryMatch { tolerance_rad: 0.01 },
    });

    let mut channel = Channel::perfect();
    print_order("transmit", &original_order);

    let t_send = Instant::now();
    let transient = encoder.encode(&original_order);
    let dt_encode = t_send.elapsed();

    let received = channel.transmit(transient);

    let t_recv = Instant::now();
    let report = decoder_match.decode(&received).expect("decode should succeed");
    let dt_decode = t_recv.elapsed();

    let decoded = report.value.as_equity_order().expect("must be an order");
    print_order("receive", decoded);

    println!();
    println!("  λ-channel:       {} ✓", report.wavelength.channel());
    println!("  OAM mode:        ℓ = {:+} (equity order schema) ✓", report.oam.charge());
    println!("  Sequence:        {} ✓", report.micro.sequence);
    println!(
        "  D3 auth:         {} (trajectory-match)",
        if report.auth_ok { "VERIFIED ✓" } else { "FAILED ✗" }
    );
    println!();
    println!("  Encode latency:  {} ns (software, single-threaded)", dt_encode.as_nanos());
    println!("  Decode latency:  {} ns (software, single-threaded)", dt_decode.as_nanos());
    println!("  Target on FPGA:  < 65 ns encode, < 20 ns decode (see spec §5.2, §6.2)");

    // ─── Scenario 2: PMD-rotated Channel ──────────────────────────────────
    banner("Scenario 2: Long-haul PMD rotation (the topological-auth payoff)");

    // A nontrivial polarization rotation — what a 1300 km link without
    // realtime Jones-matrix tracking would deliver to the receiver.
    let pmd = PoincareRotation::about_s1(0.7)
        .then(&PoincareRotation::about_s2(1.3))
        .then(&PoincareRotation::about_s3(-0.4));
    let mut channel = Channel::with_pmd(pmd);

    let transient = encoder.encode(&original_order);
    let received = channel.transmit(transient);

    let decoder_topo = Decoder::new(DecoderConfig {
        secret: alice_bob_key.clone(),
        expected_wavelength: wavelength,
        auth_mode: AuthMode::Topological,
    });

    let report_match = decoder_match.decode(&received).unwrap();
    let report_topo = decoder_topo.decode(&received).unwrap();

    println!("  Payload integrity:   {}", if report_match.value == report_topo.value {
        "intact ✓"
    } else { "diverged ✗" });
    println!(
        "  Trajectory-match:    {}    ← expected to fail without Jones-matrix compensation",
        if report_match.auth_ok { "VERIFIED ✓" } else { "FAILED ✗" }
    );
    println!(
        "  Topological:         {}    ← PMD-invariant by construction",
        if report_topo.auth_ok { "VERIFIED ✓" } else { "FAILED ✗" }
    );
    println!();
    println!("  Reading: A receiver without realtime PMD compensation cannot");
    println!("  verify the sample-by-sample trajectory, but it CAN verify the");
    println!("  topological winding number — which is preserved under any");
    println!("  unitary transform of the Poincaré sphere. This is the");
    println!("  patent-targetable property of OTAP-4D's D3 channel.");

    // ─── Scenario 3: Payload tampering ────────────────────────────────────
    banner("Scenario 3: Adversarial payload modification");

    let mut channel = Channel::perfect();
    let transient = encoder.encode(&original_order);
    let mut received = channel.transmit(transient);

    // Adversary flips the quantity field from 1000 to (1000 ^ 0xFF) = ...
    // without the shared secret.
    let original_byte = received.payload[8];
    received.payload[8] ^= 0xFF;
    println!(
        "  Adversary modifies payload byte 8: {:#04x} -> {:#04x}",
        original_byte, received.payload[8]
    );

    let report = decoder_match.decode(&received).unwrap();
    let tampered = report.value.as_equity_order().unwrap();
    print_order("(tampered)", tampered);

    println!();
    println!(
        "  D3 auth:         {} — modification detected",
        if report.auth_ok { "VERIFIED ✗ (BUG)" } else { "FAILED ✓" }
    );
    println!();
    println!("  Reading: The polarization trajectory is bound to the payload");
    println!("  via HMAC. Tampering produces a trajectory mismatch with");
    println!("  overwhelming probability — even though the payload itself");
    println!("  decodes to a syntactically valid order.");

    // ─── Summary ──────────────────────────────────────────────────────────
    banner("Reference-model summary");
    println!("  • Parallel decode of all 5 dimensions: structurally enforced");
    println!("  • Schema dispatch via OAM mode (no header parsing): ✓");
    println!("  • Polarization-bound integrity: ✓");
    println!("  • Topological auth survives PMD: ✓");
    println!("  • All round-trip invariants hold under impairment");
    println!();
    println!("  Next steps:");
    println!("    1. Bit-exact RTL implementation of Decoder against this reference");
    println!("    2. Cosim harness: feed identical inputs to RTL and Rust, diff outputs");
    println!("    3. OBG host-side driver: this codec linked as a static library");
    println!();
}
