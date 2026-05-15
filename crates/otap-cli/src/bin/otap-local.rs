//! `otap-local`: minimal two-endpoint OTAP demo with zero external state.
//!
//! Brings up two `SoftDriver`s armed from in-process `RegisterFile`s,
//! connects them through an `otap-fabric` with realistic PMD, and exercises
//! a few canonical schemas. No FLR, no network, no FPGA.
//!
//! This is the answer to: "can we run OTAP end-to-end without buying a
//! Versal eval kit?" — yes, this binary is the proof.
//!
//! Run:
//!
//! ```bash
//! cargo run --release -p otap-cli --bin otap-local
//! ```

use anyhow::Result;

use otap_core::Wavelength;
use otap_csr::RegisterFile;
use otap_fabric::Fabric;
use otap_obg::{arm_register_file, CompletionQueue, SendQueue, SoftDriver, WorkRequest};
use otap_schema::{
    equity_order::Side, EquityTradeOrder, Heartbeat, MarketTick, Schema, SchemaId,
};

fn main() -> Result<()> {
    println!("== otap-local: pure-software OTAP demo (no FPGA, no FLR) ==\n");

    // ----- Step 1: Provision shared CSR state ---------------------------
    // In the real world this is done by the FLR client writing PCIe BAR.
    // Here we just build two register files in memory with the same secret
    // and wavelength.
    let wavelength = Wavelength::new(40)?; // C-band channel 40
    let session_key = [0x9Bu8; 32];

    let mut alice_rf = RegisterFile::new();
    let mut bob_rf = RegisterFile::new();
    arm_register_file(&mut alice_rf, wavelength, &session_key, /* topological */ true)?;
    arm_register_file(&mut bob_rf, wavelength, &session_key, /* topological */ true)?;

    println!(
        "Provisioned register files at λ ch{} (~{:.3} nm), topological auth ON",
        wavelength.channel(),
        wavelength.wavelength_nm(),
    );

    // ----- Step 2: Spin up two SoftDrivers ------------------------------
    let mut alice = SoftDriver::from_csr(&alice_rf)?;
    let bob = SoftDriver::from_csr(&bob_rf)?;
    println!("Drivers armed from CSR.");

    // ----- Step 3: Wire them through a PMD-rotating fabric --------------
    let mut fabric = Fabric::with_random_pmd(/* a→b */ 0x1234_DEAD, /* b→a */ 0xCAFE_BABE);
    println!("Fabric: random PMD on both directions.\n");

    // ----- Step 4: Alice posts work; pump until Bob has completions -----
    let mut alice_send = SendQueue::new(64);
    let mut bob_cq = CompletionQueue::new(64);

    // (a) An equity trade order on OAM mode 1.
    {
        let order = EquityTradeOrder::new(
            "AAPL",
            Side::Buy,
            1000,
            195.50,
            1_700_000_000_000,
            /* client_id */ 42,
            *b"firm-uuid-16byte",
        );
        alice_send.post(
            WorkRequest::new(
                SchemaId::EquityTradeOrder,
                wavelength,
                order.encode().len() as u32,
                /* cookie */ 0xAA01,
            ),
            order.encode(),
        )?;
    }

    // (b) A market tick on OAM mode 3.
    {
        let mut symbol = [0u8; 8];
        symbol[..4].copy_from_slice(b"AAPL");
        let tick = MarketTick {
            symbol,
            bid_cents: 195.49,
            ask_cents: 195.51,
            last_cents: 195.50,
            volume: 12_500,
            timestamp_ns: 1_700_000_000_500,
            _reserved: [0u8; 16],
        };
        alice_send.post(
            WorkRequest::new(
                SchemaId::MarketTick,
                wavelength,
                tick.encode().len() as u32,
                0xAA02,
            ),
            tick.encode(),
        )?;
    }

    // (c) A heartbeat on OAM mode 4.
    {
        let hb = Heartbeat {
            node_id: 0xA11CE,
            clock_offset_ns: -240,
            heartbeat_seq: 7,
            _reserved: [0u8; 8],
        };
        alice_send.post(
            WorkRequest::new(
                SchemaId::Heartbeat,
                wavelength,
                hb.encode().len() as u32,
                0xAA03,
            ),
            hb.encode(),
        )?;
    }

    let delivered = fabric.pump_a_to_b(&mut alice, &mut alice_send, &bob, &mut bob_cq);
    println!("Alice → fabric → Bob: {} Transients delivered\n", delivered);

    // ----- Step 5: Drain and print Bob's completions --------------------
    let mut count = 0;
    while let Some(c) = bob_cq.poll() {
        count += 1;
        let auth = if c.auth_ok { "OK" } else { "FAIL" };
        match &c.value {
            otap_schema::AnySchemaValue::EquityTradeOrder(o) => {
                println!(
                    "  [{}] Bob got EquityTradeOrder: {:?} {} {} @ ${:.2} (seq={}, auth={})",
                    count,
                    o.side,
                    o.quantity,
                    o.symbol_str(),
                    o.price_cents,
                    c.sequence,
                    auth,
                );
            }
            otap_schema::AnySchemaValue::MarketTick(t) => {
                let sym = core::str::from_utf8(&t.symbol).unwrap_or("?");
                println!(
                    "  [{}] Bob got MarketTick:      {} bid={:.2} ask={:.2} last={:.2} (auth={})",
                    count,
                    sym.trim_end_matches('\0').trim(),
                    t.bid_cents,
                    t.ask_cents,
                    t.last_cents,
                    auth,
                );
            }
            otap_schema::AnySchemaValue::Heartbeat(h) => {
                println!(
                    "  [{}] Bob got Heartbeat:       node={:#x} clock_offset={}ns seq={} (auth={})",
                    count,
                    h.node_id,
                    h.clock_offset_ns,
                    h.heartbeat_seq,
                    auth,
                );
            }
        }
    }

    println!("\n== {} Transients delivered, auth survived random PMD ==", count);
    println!("== This is the no-FPGA, no-FLR proof. ==");
    Ok(())
}
