//! # otap-core
//!
//! Core protocol primitives for OTAP (Optical Transient Application Protocol).
//!
//! This crate defines the five physical dimensions of an OTAP Transient as
//! concrete Rust types. It is the *reference model* for the protocol: every
//! RTL implementation must produce bit-exact output equivalent to the encode
//! and decode paths defined here.
//!
//! ## Dimensions
//!
//! - [`Wavelength`] (D2): destination addressing via DWDM channel.
//! - [`PolarizationTrajectory`] (D3): source authentication + payload integrity.
//! - [`OamMode`] (D4): application schema selector.
//! - [`MicroStructure`] (D5): sequence number + flow control.
//! - The payload itself is held by the codec; this crate defines the carrier.
//!
//! ## Design invariants
//!
//! 1. A `Transient` is *self-describing*: every field needed to route, authenticate,
//!    and decode it is present in the value itself. There is no external context.
//! 2. Decode is *parallel*: no field depends on another for its interpretation.
//!    The `Decoder` enforces this structurally (see `otap-codec`).
//! 3. The type system enforces protocol invariants at compile time wherever possible.

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

pub mod dimensions;
pub mod transient;
pub mod error;

pub use dimensions::{
    GuardInterval, MicroStructure, OamMode, PolarizationTrajectory, StokesVector, Wavelength,
    TRAJECTORY_SAMPLES,
};
pub use error::{ProtocolError, Result};
pub use transient::Transient;
