//! `otap-fullstack`: end-to-end OTAP demo that talks to a live FLR.
//!
//! Pipeline:
//!
//!  1. Connect to a running `flrd` on `--flr` (default http://localhost:8080).
//!  2. Fetch the operator's active ApplicationSchemas via REST.
//!  3. Program Alice's RegisterFile from FLR state (`FlrClient::program_register_file`).
//!  4. Program Bob's RegisterFile from FLR state.
//!  5. Spin up two SoftDrivers from those register files.
//!  6. Connect them through a Fabric with random PMD.
//!  7. Post a few work requests, pump the fabric, drain completions.
//!
//! Run order:
//!
//! ```bash
//! # Terminal 1: start the FLR
//! cd flr && go run ./cmd/flrd -operator op-alice
//!
//! # Terminal 2: seed schemas into the FLR
//! cd flr && go run ./cmd/flr-seed -operator op-alice
//!
//! # Terminal 3: run the demo
//! cargo run --release -p otap-cli --bin otap-fullstack -- --operator op-alice
//! ```

use std::env;

use anyhow::{Context, Result};

use otap_core::Wavelength;
use otap_csr::RegisterFile;
use otap_fabric::Fabric;
use otap_flr_client::{FlrClient, FlrClientConfig};
use otap_obg::{CompletionQueue, SendQueue, SoftDriver, WorkRequest};
use otap_schema::{equity_order::Side, EquityTradeOrder, Schema, SchemaId};

struct Args {
    flr_url: String,
    operator: String,
}

fn parse_args() -> Args {
    let mut flr_url = "http://localhost:8080".to_string();
    let mut operator = "op-alice".to_string();
    let mut argv = env::args().skip(1);
    while let Some(arg) = argv.next() {
        match arg.as_str() {
            "--flr" => flr_url = argv.next().unwrap_or(flr_url),
            "--operator" => operator = argv.next().unwrap_or(operator),
            _ => {}
        }
    }
    Args { flr_url, operator }
}

fn main() -> Result<()> {
    let args = parse_args();
    println!("== otap-fullstack: live FLR-driven OTAP demo ==");
    println!("FLR URL: {}", args.flr_url);
    println!("Operator: {}\n", args.operator);

    // ----- Step 1: FLR client -------------------------------------------
    let cfg = FlrClientConfig {
        base_url: args.flr_url.clone(),
        operator_id: args.operator.clone(),
        request_timeout: std::time::Duration::from_secs(5),
        refresh_interval: std::time::Duration::from_secs(15),
    };
    let flr = FlrClient::new(cfg).context("create FLR client")?;

    // ----- Step 2: Fetch schemas (sanity) -------------------------------
    let schemas = flr
        .list_active_schemas()
        .context("list_active_schemas — is flrd running and seeded?")?;
    println!("Fetched {} active schema(s) from FLR:", schemas.len());
    for s in &schemas {
        println!(
            "  - {}: oam={}, payload={}B, status={:?}",
            s.name, s.oam_mode, s.payload_bytes, s.status,
        );
    }
    if schemas.is_empty() {
        anyhow::bail!("no active schemas; run `flr-seed -operator {}`", args.operator);
    }

    // ----- Step 3: Program both endpoints' register files ---------------
    let wavelength = Wavelength::new(40)?;
    let session_key = [0xBEu8; 32];

    let mut alice_rf = RegisterFile::new();
    let mut bob_rf = RegisterFile::new();
    let mapped_a = flr.program_register_file(&mut alice_rf, wavelength, &session_key, /*topo=*/ true)?;
    let mapped_b = flr.program_register_file(&mut bob_rf, wavelength, &session_key, /*topo=*/ true)?;
    println!(
        "\nProgrammed Alice ({} schemas) and Bob ({} schemas) from FLR.\n",
        mapped_a, mapped_b
    );

    // ----- Step 4: SoftDrivers + Fabric ---------------------------------
    let mut alice = SoftDriver::from_csr(&alice_rf)?;
    let bob = SoftDriver::from_csr(&bob_rf)?;
    let mut fabric = Fabric::with_random_pmd(0xDEAD_BEEF, 0xFEED_FACE);

    // ----- Step 5: Post some work and pump ------------------------------
    let mut alice_send = SendQueue::new(64);
    let mut bob_cq = CompletionQueue::new(64);

    for i in 0..5 {
        let order = EquityTradeOrder::new(
            "AAPL",
            if i % 2 == 0 { Side::Buy } else { Side::Sell },
            100 + i * 50,
            195.50 + (i as f64) * 0.25,
            1_700_000_000_000 + i as u64 * 1_000_000,
            42 + i as u64,
            *b"firm-uuid-16byte",
        );
        alice_send.post(
            WorkRequest::new(
                SchemaId::EquityTradeOrder,
                wavelength,
                order.encode().len() as u32,
                0xABCD_0000 | i as u64,
            ),
            order.encode(),
        )?;
    }

    let delivered = fabric.pump_a_to_b(&mut alice, &mut alice_send, &bob, &mut bob_cq);
    println!("Pumped {} Transients Alice → Bob.\n", delivered);

    // ----- Step 6: Drain completions ------------------------------------
    let mut count = 0;
    while let Some(c) = bob_cq.poll() {
        count += 1;
        let auth = if c.auth_ok { "OK" } else { "FAIL" };
        if let otap_schema::AnySchemaValue::EquityTradeOrder(o) = &c.value {
            println!(
                "  [{}] {:?} {} {} @ ${:.2} (seq={}, auth={})",
                count,
                o.side,
                o.quantity,
                o.symbol_str(),
                o.price_cents,
                c.sequence,
                auth,
            );
        }
    }
    println!("\n== full-stack demo OK, {} completions, auth survived PMD ==", count);
    Ok(())
}
