//! # otap-fabric
//!
//! Software replacement for the photonic fiber + transponder pair that would
//! otherwise sit between two OBGs. This is the centerpiece of the FPGA-bypass
//! demo path: instead of requiring a Versal eval kit, two SLMs, and a coil of
//! fiber, the entire OTAP protocol is exercised end-to-end in one process by
//! piping Transients between two [`SoftDriver`](otap_obg::SoftDriver)s through
//! an [`otap_sim::Channel`].
//!
//! ## Topology
//!
//! ```text
//!  Alice (Application)        Bob (Application)
//!         |                          ^
//!         v                          |
//!     SendQueue                CompletionQueue
//!         |                          ^
//!         v                          |
//!  +-------------+   Channel   +-------------+
//!  | SoftDriver  | ----------> | SoftDriver  |
//!  | (TX side)   |    (PMD)    | (RX side)   |
//!  +-------------+             +-------------+
//!         ^                          |
//!         |                          v
//!  CompletionQueue              SendQueue
//!         |                          |
//!  Alice (Application)        Bob (Application)
//! ```
//!
//! A `Fabric` is *bidirectional*: each end has both a SendQueue and a
//! CompletionQueue, and the same `Channel` model serves both directions. In
//! production the forward/reverse channels are physically separate fibers,
//! so a `Fabric` may be configured with two independent `Channel`s.
//!
//! ## FPGA mapping
//!
//! In the production hardware path, this entire crate disappears. The two
//! ends of the fabric are replaced by:
//!
//!  - Alice's FPGA TX pipeline writes to its 800ZR transponder.
//!  - Light propagates through fiber + AWG to Bob's transponder.
//!  - Bob's FPGA RX pipeline reads the demodulated samples.
//!
//! Everything *above* the fabric layer (work queues, schemas, FLR config) is
//! unchanged, which is the point: the software stack is the same one we ship
//! to the FPGA.

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use otap_core::Transient;
use otap_obg::{CompletionQueue, SendQueue, SoftDriver};
use otap_sim::Channel;

/// A bidirectional simulated fiber connecting two `SoftDriver` endpoints.
///
/// The `Fabric` does not own the drivers themselves — that would make it
/// impossible for applications to post work requests independently. Instead,
/// it owns the two channels (one per direction) and exposes `pump_*` methods
/// that the caller wires up to its main loop.
pub struct Fabric {
    /// Channel from endpoint A → endpoint B.
    pub a_to_b: Channel,
    /// Channel from endpoint B → endpoint A.
    pub b_to_a: Channel,
}

impl Fabric {
    /// Construct a fabric from two channel models.
    pub fn new(a_to_b: Channel, b_to_a: Channel) -> Self {
        Self { a_to_b, b_to_a }
    }

    /// Construct a fabric with two perfect (identity, zero-BER) channels.
    pub fn perfect() -> Self {
        Self::new(Channel::perfect(), Channel::perfect())
    }

    /// Construct a fabric with random-PMD channels (different seeds per direction).
    /// This is the realistic default for demo runs.
    pub fn with_random_pmd(seed_ab: u64, seed_ba: u64) -> Self {
        Self::new(Channel::random_pmd(seed_ab), Channel::random_pmd(seed_ba))
    }

    /// Pump one direction: A → B.
    ///
    /// Drains A's send queue, encodes each work request into a Transient,
    /// transmits through the A→B channel, and delivers each post-channel
    /// Transient to B's `SoftDriver::receive`.
    ///
    /// Returns the number of Transients delivered.
    pub fn pump_a_to_b(
        &mut self,
        a_driver: &mut SoftDriver,
        a_send: &mut SendQueue,
        b_driver: &SoftDriver,
        b_cq: &mut CompletionQueue,
    ) -> usize {
        let outgoing = a_driver.transmit_pending(a_send);
        let n = outgoing.len();
        for (transient, _cookie) in outgoing {
            let post_channel: Transient = self.a_to_b.transmit(transient);
            b_driver.receive(&post_channel, b_cq);
        }
        n
    }

    /// Pump one direction: B → A.
    ///
    /// Symmetric to [`Fabric::pump_a_to_b`]; uses the `b_to_a` channel.
    pub fn pump_b_to_a(
        &mut self,
        b_driver: &mut SoftDriver,
        b_send: &mut SendQueue,
        a_driver: &SoftDriver,
        a_cq: &mut CompletionQueue,
    ) -> usize {
        let outgoing = b_driver.transmit_pending(b_send);
        let n = outgoing.len();
        for (transient, _cookie) in outgoing {
            let post_channel: Transient = self.b_to_a.transmit(transient);
            a_driver.receive(&post_channel, a_cq);
        }
        n
    }

    /// Pump both directions once.
    ///
    /// Returns `(a_to_b_count, b_to_a_count)`.
    pub fn pump_both(
        &mut self,
        a_driver: &mut SoftDriver,
        a_send: &mut SendQueue,
        a_cq: &mut CompletionQueue,
        b_driver: &mut SoftDriver,
        b_send: &mut SendQueue,
        b_cq: &mut CompletionQueue,
    ) -> (usize, usize) {
        let ab = self.pump_a_to_b(a_driver, a_send, b_driver, b_cq);
        let ba = self.pump_b_to_a(b_driver, b_send, a_driver, a_cq);
        (ab, ba)
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use otap_core::Wavelength;
    use otap_csr::RegisterFile;
    use otap_obg::{arm_register_file, WorkRequest};
    use otap_schema::{equity_order::Side, EquityTradeOrder, Schema, SchemaId};

    fn shared_rf(channel: u16) -> RegisterFile {
        let mut rf = RegisterFile::new();
        let key = [0x5Au8; 32];
        arm_register_file(&mut rf, Wavelength::new(channel).unwrap(), &key, false).unwrap();
        rf
    }

    #[test]
    fn alice_to_bob_perfect_channel() {
        let rf = shared_rf(40);
        let mut alice = SoftDriver::from_csr(&rf).unwrap();
        let bob = SoftDriver::from_csr(&rf).unwrap();
        let mut fabric = Fabric::perfect();

        let mut a_send = SendQueue::new(8);
        let mut b_cq = CompletionQueue::new(8);

        let order = EquityTradeOrder::new("MSFT", Side::Buy, 500, 425.0, 0, 7, [0u8; 16]);
        a_send
            .post(
                WorkRequest::new(
                    SchemaId::EquityTradeOrder,
                    Wavelength::new(40).unwrap(),
                    order.encode().len() as u32,
                    7,
                ),
                order.encode(),
            )
            .unwrap();

        let delivered = fabric.pump_a_to_b(&mut alice, &mut a_send, &bob, &mut b_cq);
        assert_eq!(delivered, 1);

        let completion = b_cq.poll().expect("bob should receive");
        assert!(completion.auth_ok, "auth must survive clean channel");
        assert_eq!(completion.value.as_equity_order().unwrap(), &order);
    }

    #[test]
    fn topological_auth_survives_random_pmd() {
        // Arm both endpoints with topological auth.
        let mut rf = RegisterFile::new();
        let key = [0x5Au8; 32];
        arm_register_file(&mut rf, Wavelength::new(40).unwrap(), &key, true).unwrap();

        let mut alice = SoftDriver::from_csr(&rf).unwrap();
        let bob = SoftDriver::from_csr(&rf).unwrap();
        let mut fabric = Fabric::with_random_pmd(12345, 67890);

        let mut a_send = SendQueue::new(8);
        let mut b_cq = CompletionQueue::new(8);

        let order = EquityTradeOrder::new("AAPL", Side::Sell, 200, 195.50, 0, 11, [0u8; 16]);
        a_send
            .post(
                WorkRequest::new(
                    SchemaId::EquityTradeOrder,
                    Wavelength::new(40).unwrap(),
                    order.encode().len() as u32,
                    11,
                ),
                order.encode(),
            )
            .unwrap();

        fabric.pump_a_to_b(&mut alice, &mut a_send, &bob, &mut b_cq);
        let completion = b_cq.poll().expect("must receive");
        assert!(
            completion.auth_ok,
            "topological auth must be invariant to PMD rotation"
        );
        assert_eq!(completion.value.as_equity_order().unwrap(), &order);
    }
}
