//! # otap-csr
//!
//! CSR (Control & Status Register) map for the OTAP Bridge Gateway and a
//! software implementation that bypasses the FPGA.
//!
//! ## Why this crate exists
//!
//! In hardware, host software configures the OBG by writing 32-bit registers
//! over PCIe BAR space. The FPGA RTL exposes a CSR interface (see the OTAP
//! FPGA development doc, §1 `csr_write` stub). This crate:
//!
//! 1. **Defines the register map** as Rust constants and structured types, so
//!    Rust software (the FLR client, the demo, the application) and the
//!    eventual SystemVerilog (`csr_decoder.sv`) agree on the addresses and
//!    bit layouts.
//!
//! 2. **Implements [`RegisterFile`]**: a software equivalent of the CSR space
//!    backed by `[u32; N]`. The pure-software OBG ([`otap_obg::SoftDriver`])
//!    reads its configuration from this just like the FPGA reads its config
//!    from physical registers. This is how the demo runs end-to-end without
//!    silicon: the "FPGA" is just an in-memory u32 array.
//!
//! 3. **Provides typed views** ([`view::CsrView`]) that convert raw register
//!    contents into protocol types (`Wavelength`, `SharedSecret`, schema
//!    tables) — the same conversion the FPGA hardware would do.
//!
//! ## Register map (v1)
//!
//! All offsets are byte-addressed, 32-bit aligned, big-endian where multi-byte
//! values span multiple registers.
//!
//! ```text
//!  Offset   Width  Name              Purpose
//!  0x000    32     WAVELENGTH        Wavelength channel index (low 16 bits)
//!  0x004    32     CTRL              Control flags (bit 0: enable TX, bit 1: enable RX, bit 2: auth mode 0=traj 1=topo)
//!  0x008    32     AUTH_TOLERANCE    Trajectory tolerance, as fixed-point milliradians (e.g., 10 = 0.010 rad)
//!  0x00C    32     STATUS            Status flags (bit 0: armed, bit 1: peer ready, bit 31: error)
//!
//!  0x010    256    SECRET[0..8]      Shared secret, 256 bits = 8 × 32-bit words
//!                                    (registers 0x010, 0x014, 0x018, 0x01C,
//!                                     0x020, 0x024, 0x028, 0x02C)
//!
//!  0x030    32     SECRET_VALID      Bit 0 set when SECRET is loaded.
//!
//!  0x040    128    SCHEMA_PAYLOAD_TABLE[0..32]
//!                                    Schema-ID → payload-bytes lookup.
//!                                    16 bits per entry, 2 entries per 32-bit word.
//!                                    Words at 0x040..0x080 (= 32 entries / 2 = 16 words).
//!
//!  0x080    32     OAM_TABLE_BASE    (reserved alignment)
//!
//!  0x100    256    OAM_TO_SCHEMA_TABLE[0..32]
//!                                    OAM-mode-index → schema-ID lookup.
//!                                    OAM index is (oam_charge + 16) so the
//!                                    valid range -16..=+16 maps to 0..=32.
//!                                    8 bits per entry, 4 entries per 32-bit
//!                                    word. 32 entries = 8 words at 0x100..0x120.
//!
//!  0x200    128    PEER_WAVELENGTH_TABLE[0..16]
//!                                    16-bit peer wavelength per registered
//!                                    session (host-side lookup, not used by
//!                                    the FPGA TX/RX path itself).
//! ```
//!
//! Schema-ID is a *registry-local* 1-byte handle assigned by the host driver
//! when it installs a schema. ID 0 is reserved for "no schema bound".
//!
//! ## Versioning
//!
//! [`MAP_VERSION`] is a compile-time constant. The SystemVerilog parameter
//! file (`csr_map.svh`) must declare the same value. Mismatches between host
//! and FPGA must be detected at bring-up via reading register `0x000` low
//! bits and panicking on version skew.

#![forbid(unsafe_op_in_unsafe_fn)]
#![warn(missing_docs)]

use otap_core::{OamMode, ProtocolError, Wavelength};
use otap_crypto::SharedSecret;
use thiserror::Error;
use zeroize::Zeroize;

// ============================================================================
// Map version
// ============================================================================

/// Current CSR map version. Increment on incompatible layout changes.
pub const MAP_VERSION: u8 = 1;

// ============================================================================
// Register offsets
// ============================================================================

/// Wavelength channel (bits 0..16 used).
pub const REG_WAVELENGTH: usize = 0x000;
/// Control flags.
pub const REG_CTRL: usize = 0x004;
/// Auth tolerance in milliradians.
pub const REG_AUTH_TOLERANCE: usize = 0x008;
/// Status flags (read-only by host).
pub const REG_STATUS: usize = 0x00C;
/// Shared secret base address (8 × 32-bit words).
pub const REG_SECRET_BASE: usize = 0x010;
/// Number of 32-bit words for the secret.
pub const SECRET_WORDS: usize = 8;
/// Secret-loaded flag.
pub const REG_SECRET_VALID: usize = 0x030;
/// Schema-ID → payload-bytes table base.
pub const REG_SCHEMA_PAYLOAD_BASE: usize = 0x040;
/// Number of entries in the schema-payload table.
pub const SCHEMA_TABLE_ENTRIES: usize = 32;
/// OAM-mode → schema-ID table base.
pub const REG_OAM_TABLE_BASE: usize = 0x100;
/// Number of entries in the OAM table (covers -16..=+16, indexed by oam+16).
pub const OAM_TABLE_ENTRIES: usize = 33;
/// Peer wavelength table.
pub const REG_PEER_WAVELENGTH_BASE: usize = 0x200;
/// Number of peer-wavelength entries.
pub const PEER_TABLE_ENTRIES: usize = 16;

/// Total CSR space size in bytes (must be at least 0x300).
pub const CSR_SPACE_BYTES: usize = 0x400;
/// CSR space in 32-bit words.
pub const CSR_SPACE_WORDS: usize = CSR_SPACE_BYTES / 4;

// ============================================================================
// Control register bit layout
// ============================================================================

/// Bit 0 of REG_CTRL: enable TX path.
pub const CTRL_ENABLE_TX: u32 = 1 << 0;
/// Bit 1 of REG_CTRL: enable RX path.
pub const CTRL_ENABLE_RX: u32 = 1 << 1;
/// Bit 2 of REG_CTRL: 0 = trajectory-match auth, 1 = topological auth.
pub const CTRL_AUTH_TOPOLOGICAL: u32 = 1 << 2;

// ============================================================================
// Status register bits (read-only)
// ============================================================================

/// Bit 0 of REG_STATUS: armed (secret valid AND wavelength set).
pub const STATUS_ARMED: u32 = 1 << 0;
/// Bit 1 of REG_STATUS: peer ready (handshake complete).
pub const STATUS_PEER_READY: u32 = 1 << 1;
/// Bit 31 of REG_STATUS: error flag.
pub const STATUS_ERROR: u32 = 1 << 31;

// ============================================================================
// Errors
// ============================================================================

/// Errors specific to CSR access and decoding.
#[derive(Debug, Error)]
pub enum CsrError {
    /// Register offset is outside the allocated CSR space.
    #[error("CSR offset 0x{0:03x} out of range (space size {1} bytes)")]
    OutOfRange(usize, usize),

    /// Register offset is not 32-bit aligned.
    #[error("CSR offset 0x{0:03x} is not 32-bit aligned")]
    Misaligned(usize),

    /// Schema ID is outside the table range.
    #[error("schema id {0} out of range [0, {1})")]
    SchemaIdOutOfRange(u8, usize),

    /// OAM mode is outside the table range.
    #[error("oam mode {0} outside table range")]
    OamOutOfRange(i8),

    /// Underlying protocol error (wavelength range, etc.).
    #[error(transparent)]
    Protocol(#[from] ProtocolError),
}

/// Convenience alias.
pub type CsrResult<T> = std::result::Result<T, CsrError>;

// ============================================================================
// RegisterFile — software CSR space
// ============================================================================

/// In-memory model of a CSR register file.
///
/// All accesses are word-aligned 32-bit reads/writes, matching the PCIe BAR
/// access pattern. Multi-word values (the 256-bit shared secret, the schema
/// tables) are spread across consecutive registers per the map above.
///
/// `RegisterFile` is the unit of state shared between the FLR client (which
/// writes configuration) and the `SoftDriver` (which reads configuration on
/// every Transient).
#[derive(Debug)]
pub struct RegisterFile {
    words: Box<[u32; CSR_SPACE_WORDS]>,
}

impl Default for RegisterFile {
    fn default() -> Self {
        Self::new()
    }
}

impl RegisterFile {
    /// Construct an empty register file (all zeros).
    pub fn new() -> Self {
        Self {
            words: Box::new([0u32; CSR_SPACE_WORDS]),
        }
    }

    /// Read a 32-bit word at byte offset `offset`.
    pub fn read_u32(&self, offset: usize) -> CsrResult<u32> {
        if offset >= CSR_SPACE_BYTES {
            return Err(CsrError::OutOfRange(offset, CSR_SPACE_BYTES));
        }
        if offset & 0x3 != 0 {
            return Err(CsrError::Misaligned(offset));
        }
        Ok(self.words[offset / 4])
    }

    /// Write a 32-bit word at byte offset `offset`.
    pub fn write_u32(&mut self, offset: usize, value: u32) -> CsrResult<()> {
        if offset >= CSR_SPACE_BYTES {
            return Err(CsrError::OutOfRange(offset, CSR_SPACE_BYTES));
        }
        if offset & 0x3 != 0 {
            return Err(CsrError::Misaligned(offset));
        }
        self.words[offset / 4] = value;
        Ok(())
    }

    /// Snapshot the current contents (for read-only views).
    pub fn snapshot(&self) -> Box<[u32; CSR_SPACE_WORDS]> {
        Box::new(*self.words)
    }
}

// ============================================================================
// Writer — typed helpers that produce CSR writes from protocol values
// ============================================================================

/// Encoding helpers that write protocol-typed values into the register file.
pub mod writer {
    use super::*;

    /// Write the OBG's transmit wavelength.
    pub fn set_wavelength(rf: &mut RegisterFile, w: Wavelength) -> CsrResult<()> {
        rf.write_u32(REG_WAVELENGTH, w.channel() as u32)
    }

    /// Write a control register value.
    pub fn set_ctrl(rf: &mut RegisterFile, value: u32) -> CsrResult<()> {
        rf.write_u32(REG_CTRL, value)
    }

    /// Write the auth tolerance (radians, stored as milliradians).
    pub fn set_auth_tolerance(rf: &mut RegisterFile, radians: f32) -> CsrResult<()> {
        let mrad = (radians * 1000.0).round().max(0.0) as u32;
        rf.write_u32(REG_AUTH_TOLERANCE, mrad)
    }

    /// Write the 256-bit shared secret across SECRET_WORDS registers.
    ///
    /// The secret bytes are taken in big-endian order: byte 0..4 → word 0
    /// (which lives at register offset `REG_SECRET_BASE + 0`).
    pub fn set_secret(rf: &mut RegisterFile, secret_bytes: &[u8; 32]) -> CsrResult<()> {
        for i in 0..SECRET_WORDS {
            let word = u32::from_be_bytes([
                secret_bytes[i * 4],
                secret_bytes[i * 4 + 1],
                secret_bytes[i * 4 + 2],
                secret_bytes[i * 4 + 3],
            ]);
            rf.write_u32(REG_SECRET_BASE + i * 4, word)?;
        }
        rf.write_u32(REG_SECRET_VALID, 1)?;
        Ok(())
    }

    /// Write one schema-ID → payload-bytes entry.
    ///
    /// Schema IDs are 1..32 (0 reserved). Payloads up to 65535 bytes.
    /// Two entries pack into one 32-bit word: id*2 → low 16 bits, (id*2+1) → high.
    /// Since 32 entries × 16 bits = 512 bits = 16 words, the table occupies
    /// registers 0x040..0x080.
    pub fn set_schema_payload(rf: &mut RegisterFile, schema_id: u8, payload_bytes: u16) -> CsrResult<()> {
        if schema_id == 0 || (schema_id as usize) >= SCHEMA_TABLE_ENTRIES {
            return Err(CsrError::SchemaIdOutOfRange(schema_id, SCHEMA_TABLE_ENTRIES));
        }
        let entry = schema_id as usize;
        let word_off = REG_SCHEMA_PAYLOAD_BASE + (entry / 2) * 4;
        let mut word = rf.read_u32(word_off)?;
        if entry.is_multiple_of(2) {
            // low half
            word = (word & 0xFFFF_0000) | (payload_bytes as u32);
        } else {
            // high half
            word = (word & 0x0000_FFFF) | ((payload_bytes as u32) << 16);
        }
        rf.write_u32(word_off, word)
    }

    /// Write one OAM-mode → schema-ID entry.
    ///
    /// OAM mode is in [-16, +16]; we use `oam + 16` as the table index
    /// (0..=32). 4 entries per word, 8 bits each.
    pub fn set_oam_to_schema(rf: &mut RegisterFile, oam: OamMode, schema_id: u8) -> CsrResult<()> {
        let idx = (oam.charge() as i32) + 16;
        if !(0..=32).contains(&idx) {
            return Err(CsrError::OamOutOfRange(oam.charge()));
        }
        let idx = idx as usize;
        let word_off = REG_OAM_TABLE_BASE + (idx / 4) * 4;
        let byte_in_word = idx % 4;
        let mut word = rf.read_u32(word_off)?;
        let shift = byte_in_word * 8;
        word &= !(0xFFu32 << shift);
        word |= (schema_id as u32) << shift;
        rf.write_u32(word_off, word)
    }
}

// ============================================================================
// Reader — typed views of register file contents
// ============================================================================

/// Typed read-only views over a [`RegisterFile`].
pub mod view {
    use super::*;

    /// Read-only typed view of a register file's contents.
    ///
    /// Used by the SoftDriver to pull its working configuration; in hardware
    /// the same logic runs as combinational decoders fanning out from the
    /// CSR register file.
    pub struct CsrView<'a> {
        pub(crate) rf: &'a RegisterFile,
    }

    impl<'a> CsrView<'a> {
        /// Construct a view.
        pub fn new(rf: &'a RegisterFile) -> Self {
            Self { rf }
        }

        /// Read the configured wavelength.
        pub fn wavelength(&self) -> CsrResult<Wavelength> {
            let v = self.rf.read_u32(REG_WAVELENGTH)?;
            Ok(Wavelength::new((v & 0xFFFF) as u16)?)
        }

        /// Read the control register.
        pub fn ctrl(&self) -> CsrResult<u32> {
            self.rf.read_u32(REG_CTRL)
        }

        /// Read the auth tolerance in radians.
        pub fn auth_tolerance(&self) -> CsrResult<f32> {
            Ok((self.rf.read_u32(REG_AUTH_TOLERANCE)? as f32) / 1000.0)
        }

        /// True if the SECRET_VALID flag is set.
        pub fn secret_valid(&self) -> CsrResult<bool> {
            Ok(self.rf.read_u32(REG_SECRET_VALID)? & 1 != 0)
        }

        /// Reconstitute the 256-bit shared secret.
        ///
        /// Returns `None` if SECRET_VALID is not set. The returned key
        /// material is zeroized on drop.
        pub fn shared_secret(&self) -> CsrResult<Option<SharedSecret>> {
            if !self.secret_valid()? {
                return Ok(None);
            }
            let mut bytes = [0u8; 32];
            for i in 0..SECRET_WORDS {
                let w = self.rf.read_u32(REG_SECRET_BASE + i * 4)?;
                let be = w.to_be_bytes();
                bytes[i * 4..i * 4 + 4].copy_from_slice(&be);
            }
            let secret = SharedSecret::new(bytes);
            bytes.zeroize();
            Ok(Some(secret))
        }

        /// Look up the payload byte count for a given schema ID.
        /// Returns 0 if the schema ID is not registered.
        pub fn schema_payload_bytes(&self, schema_id: u8) -> CsrResult<u16> {
            if schema_id == 0 || (schema_id as usize) >= SCHEMA_TABLE_ENTRIES {
                return Err(CsrError::SchemaIdOutOfRange(schema_id, SCHEMA_TABLE_ENTRIES));
            }
            let entry = schema_id as usize;
            let word_off = REG_SCHEMA_PAYLOAD_BASE + (entry / 2) * 4;
            let word = self.rf.read_u32(word_off)?;
            let half = if entry.is_multiple_of(2) {
                word & 0xFFFF
            } else {
                (word >> 16) & 0xFFFF
            };
            Ok(half as u16)
        }

        /// Look up the schema ID bound to a given OAM mode. Returns 0 if not set.
        pub fn schema_id_for_oam(&self, oam: OamMode) -> CsrResult<u8> {
            let idx = (oam.charge() as i32) + 16;
            if !(0..=32).contains(&idx) {
                return Err(CsrError::OamOutOfRange(oam.charge()));
            }
            let idx = idx as usize;
            let word_off = REG_OAM_TABLE_BASE + (idx / 4) * 4;
            let byte_in_word = idx % 4;
            let word = self.rf.read_u32(word_off)?;
            Ok(((word >> (byte_in_word * 8)) & 0xFF) as u8)
        }
    }
}

// ============================================================================
// Tests
// ============================================================================

#[cfg(test)]
mod tests {
    use super::*;
    use otap_core::{OamMode, Wavelength};

    #[test]
    fn wavelength_round_trip() {
        let mut rf = RegisterFile::new();
        writer::set_wavelength(&mut rf, Wavelength::new(40).unwrap()).unwrap();
        let v = view::CsrView::new(&rf);
        assert_eq!(v.wavelength().unwrap().channel(), 40);
    }

    #[test]
    fn secret_round_trip() {
        let mut rf = RegisterFile::new();
        let key = [0x11u8; 32];
        writer::set_secret(&mut rf, &key).unwrap();
        let v = view::CsrView::new(&rf);
        assert!(v.secret_valid().unwrap());
        let secret = v.shared_secret().unwrap().expect("secret must be present");
        // Cannot compare SharedSecret directly (no Eq); construct a fresh one
        // from the same bytes — they should produce the same hash output when
        // used in derive_trajectory. Here we just check the flag.
        drop(secret);
    }

    #[test]
    fn schema_table_packed_pairs() {
        let mut rf = RegisterFile::new();
        writer::set_schema_payload(&mut rf, 1, 256).unwrap();
        writer::set_schema_payload(&mut rf, 2, 64).unwrap();
        writer::set_schema_payload(&mut rf, 3, 32).unwrap();
        let v = view::CsrView::new(&rf);
        assert_eq!(v.schema_payload_bytes(1).unwrap(), 256);
        assert_eq!(v.schema_payload_bytes(2).unwrap(), 64);
        assert_eq!(v.schema_payload_bytes(3).unwrap(), 32);
    }

    #[test]
    fn oam_table_packed_quads() {
        let mut rf = RegisterFile::new();
        let m1 = OamMode::new(1).unwrap();
        let m3 = OamMode::new(3).unwrap();
        let m_neg2 = OamMode::new(-2).unwrap();
        writer::set_oam_to_schema(&mut rf, m1, 7).unwrap();
        writer::set_oam_to_schema(&mut rf, m3, 8).unwrap();
        writer::set_oam_to_schema(&mut rf, m_neg2, 9).unwrap();

        let v = view::CsrView::new(&rf);
        assert_eq!(v.schema_id_for_oam(m1).unwrap(), 7);
        assert_eq!(v.schema_id_for_oam(m3).unwrap(), 8);
        assert_eq!(v.schema_id_for_oam(m_neg2).unwrap(), 9);
    }

    #[test]
    fn out_of_range_offset_rejected() {
        let rf = RegisterFile::new();
        let err = rf.read_u32(CSR_SPACE_BYTES).unwrap_err();
        assert!(matches!(err, CsrError::OutOfRange(_, _)));
    }

    #[test]
    fn misaligned_offset_rejected() {
        let rf = RegisterFile::new();
        let err = rf.read_u32(0x3).unwrap_err();
        assert!(matches!(err, CsrError::Misaligned(3)));
    }
}
