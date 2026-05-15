//! The [`Transient`] — the atomic protocol unit.
//!
//! A Transient corresponds to one optical pulse train carrying simultaneously:
//! - **What** (amplitude → payload)
//! - **Where** (wavelength → destination)
//! - **Who** (polarization trajectory → source identity + integrity)
//! - **What kind** (OAM → application schema)
//! - **Order** (microstructure → sequence + flow)
//!
//! ## On the parallel-decode invariant
//!
//! In the reference model, a Transient is a struct with five independently-typed
//! fields. The decoder reads them in parallel — no field is on the critical path
//! of another. The RTL implementation must preserve this property.

use crate::dimensions::{
    MicroStructure, OamMode, PolarizationTrajectory, Wavelength,
};

/// One OTAP Transient as it exists between encode and decode.
///
/// The `payload` is intentionally a raw byte slice at this layer: the schema
/// crate provides typed views per OAM mode. The codec ensures the payload's
/// length matches the schema; this struct itself is schema-agnostic.
#[derive(Debug, Clone, PartialEq)]
pub struct Transient {
    /// D2 — destination addressing.
    pub wavelength: Wavelength,
    /// D3 — source authentication + integrity.
    pub polarization: PolarizationTrajectory,
    /// D4 — application schema selector.
    pub oam: OamMode,
    /// D5 — sequence number + flow control.
    pub micro: MicroStructure,
    /// D1 — payload bits (amplitude-modulated).
    ///
    /// Length is determined by the schema for `oam`. The codec validates this.
    pub payload: Vec<u8>,
}

impl Transient {
    /// Construct a Transient from its dimensional components.
    ///
    /// This is a low-level constructor; in practice, use
    /// [`otap_codec::Encoder`](../../otap_codec/struct.Encoder.html) which
    /// derives the polarization trajectory from the payload and key material.
    pub fn new(
        wavelength: Wavelength,
        polarization: PolarizationTrajectory,
        oam: OamMode,
        micro: MicroStructure,
        payload: Vec<u8>,
    ) -> Self {
        Self {
            wavelength,
            polarization,
            oam,
            micro,
            payload,
        }
    }

    /// Total wire size in bytes (payload + dimension metadata).
    ///
    /// Note: this is the *reference* size. On the actual optical channel,
    /// dimensions D2–D5 occupy no payload bytes — they are carried in
    /// orthogonal physical properties of the light. In the software model,
    /// the wire format must serialize them, but this overhead does not
    /// exist on real fiber.
    pub fn reference_size_bytes(&self) -> usize {
        self.payload.len()
    }
}
