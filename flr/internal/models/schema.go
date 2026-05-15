// Package models — ApplicationSchema and field-layout types for the FLR
// schema registry. A schema is a signed, Merkle-anchored declaration that
// "OAM mode ℓ at operator O carries payload P with field layout F". The OBG
// (Rust side) consumes signed schemas to populate its decode pipeline.
package models

import (
	"fmt"
	"time"
)

// SchemaStatus indicates the lifecycle state of an ApplicationSchema.
type SchemaStatus int32

const (
	// SchemaStatusUnspecified is the zero value (do not use).
	SchemaStatusUnspecified SchemaStatus = 0
	// SchemaStatusActive: this schema is the live binding for its OAM mode.
	SchemaStatusActive SchemaStatus = 1
	// SchemaStatusDeprecated: schema is still valid for decode but new TX should use a newer ID.
	SchemaStatusDeprecated SchemaStatus = 2
	// SchemaStatusRevoked: schema is no longer valid; receivers should reject Transients claiming it.
	SchemaStatusRevoked SchemaStatus = 3
)

func (s SchemaStatus) String() string {
	switch s {
	case SchemaStatusActive:
		return "ACTIVE"
	case SchemaStatusDeprecated:
		return "DEPRECATED"
	case SchemaStatusRevoked:
		return "REVOKED"
	default:
		return "UNSPECIFIED"
	}
}

// FieldType enumerates the primitive field types a schema can contain.
// These are the *only* types — schemas are intentionally not Turing complete.
// Anything more complex must be expressed as a sequence of primitives.
type FieldType int32

const (
	// FieldTypeUnspecified is the zero value (do not use).
	FieldTypeUnspecified FieldType = 0
	// FieldTypeU8: 1 byte unsigned.
	FieldTypeU8 FieldType = 1
	// FieldTypeU16: 2 bytes unsigned big-endian.
	FieldTypeU16 FieldType = 2
	// FieldTypeU32: 4 bytes unsigned big-endian.
	FieldTypeU32 FieldType = 3
	// FieldTypeU64: 8 bytes unsigned big-endian.
	FieldTypeU64 FieldType = 4
	// FieldTypeI8: 1 byte signed.
	FieldTypeI8 FieldType = 5
	// FieldTypeI16: 2 bytes signed big-endian.
	FieldTypeI16 FieldType = 6
	// FieldTypeI32: 4 bytes signed big-endian.
	FieldTypeI32 FieldType = 7
	// FieldTypeI64: 8 bytes signed big-endian.
	FieldTypeI64 FieldType = 8
	// FieldTypeF32: 4 bytes IEEE-754 single big-endian.
	FieldTypeF32 FieldType = 9
	// FieldTypeF64: 8 bytes IEEE-754 double big-endian.
	FieldTypeF64 FieldType = 10
	// FieldTypeAscii: fixed-length ASCII bytes, null-padded.
	FieldTypeAscii FieldType = 11
	// FieldTypeBytes: opaque fixed-length bytes (UUIDs, hashes, reserved zones).
	FieldTypeBytes FieldType = 12
	// FieldTypeEnumU8: 1-byte enumeration; semantics defined by Name string.
	FieldTypeEnumU8 FieldType = 13
)

func (f FieldType) String() string {
	names := map[FieldType]string{
		FieldTypeU8: "u8", FieldTypeU16: "u16", FieldTypeU32: "u32", FieldTypeU64: "u64",
		FieldTypeI8: "i8", FieldTypeI16: "i16", FieldTypeI32: "i32", FieldTypeI64: "i64",
		FieldTypeF32: "f32", FieldTypeF64: "f64",
		FieldTypeAscii: "ascii", FieldTypeBytes: "bytes", FieldTypeEnumU8: "enum_u8",
	}
	if s, ok := names[f]; ok {
		return s
	}
	return "UNSPECIFIED"
}

// SizeBytes returns the on-wire size of this primitive type. For variable-
// width types (ascii, bytes, enum_u8) the schema FieldDef carries an explicit
// Length; for fixed types Length must equal SizeBytes() or be 0.
func (f FieldType) SizeBytes() int {
	switch f {
	case FieldTypeU8, FieldTypeI8, FieldTypeEnumU8:
		return 1
	case FieldTypeU16, FieldTypeI16:
		return 2
	case FieldTypeU32, FieldTypeI32, FieldTypeF32:
		return 4
	case FieldTypeU64, FieldTypeI64, FieldTypeF64:
		return 8
	default:
		// Variable-width types — caller must consult FieldDef.Length.
		return -1
	}
}

// IsFixedWidth reports whether the type has a single canonical width.
func (f FieldType) IsFixedWidth() bool {
	return f.SizeBytes() > 0
}

// FieldDef describes one field in an ApplicationSchema's payload.
//
// The full payload is the concatenation of these fields in offset order with
// no padding between them beyond what the schema author chose. Reserved/padding
// zones are expressed as FieldTypeBytes with a chosen Length.
type FieldDef struct {
	// Name is a human-readable identifier (e.g., "symbol", "quantity").
	Name string `json:"name"`
	// Offset is the byte offset within the payload where this field starts.
	Offset int32 `json:"offset"`
	// Length is the byte length of this field. Must equal Type.SizeBytes() for
	// fixed-width types, or be the chosen size for ascii/bytes/enum_u8.
	Length int32 `json:"length"`
	// Type is the on-wire encoding of this field.
	Type FieldType `json:"type"`
	// Description is optional free-text doc, omitted from canonical hash input.
	Description string `json:"description,omitempty"`
}

// Validate returns an error if the FieldDef is internally inconsistent.
func (f *FieldDef) Validate() error {
	if f.Name == "" {
		return fmt.Errorf("field name cannot be empty")
	}
	if f.Length <= 0 {
		return fmt.Errorf("field %q length must be positive (got %d)", f.Name, f.Length)
	}
	if f.Offset < 0 {
		return fmt.Errorf("field %q offset cannot be negative (got %d)", f.Name, f.Offset)
	}
	if f.Type.IsFixedWidth() && int(f.Length) != f.Type.SizeBytes() {
		return fmt.Errorf("field %q type %s requires length %d, got %d",
			f.Name, f.Type, f.Type.SizeBytes(), f.Length)
	}
	return nil
}

// FieldLayout is the ordered set of fields constituting a schema payload.
type FieldLayout struct {
	Fields []FieldDef `json:"fields"`
}

// TotalBytes computes the total payload size implied by the layout
// (max offset+length across all fields).
func (l *FieldLayout) TotalBytes() int32 {
	var maxEnd int32
	for _, f := range l.Fields {
		end := f.Offset + f.Length
		if end > maxEnd {
			maxEnd = end
		}
	}
	return maxEnd
}

// Validate returns an error if the layout has overlapping fields, gaps that
// aren't covered by reserved-bytes fields, or invalid FieldDefs.
func (l *FieldLayout) Validate() error {
	if len(l.Fields) == 0 {
		return fmt.Errorf("layout must have at least one field")
	}
	// Per-field validity
	for i := range l.Fields {
		if err := l.Fields[i].Validate(); err != nil {
			return fmt.Errorf("field[%d]: %w", i, err)
		}
	}
	// Check non-overlap. Build occupancy map up to TotalBytes.
	total := l.TotalBytes()
	occupied := make([]bool, total)
	for _, f := range l.Fields {
		for b := f.Offset; b < f.Offset+f.Length; b++ {
			if b < 0 || b >= total {
				return fmt.Errorf("field %q: byte %d out of layout bounds [0,%d)",
					f.Name, b, total)
			}
			if occupied[b] {
				return fmt.Errorf("field %q overlaps another field at byte %d", f.Name, b)
			}
			occupied[b] = true
		}
	}
	// All bytes must be covered (no gaps).
	for b, ok := range occupied {
		if !ok {
			return fmt.Errorf("byte %d in layout is not covered by any field "+
				"(use a `bytes` field for reserved/padding zones)", b)
		}
	}
	return nil
}

// ApplicationSchema is a signed, registry-anchored declaration of a payload
// schema bound to an OAM mode. The OBG receives schemas via the FLR client
// and uses them to drive parallel decode pipelines.
//
// Multiple schemas may target the same OAM mode over time (versioning); only
// one should be SchemaStatusActive at a time per OAM mode within an operator.
// Cross-operator schema collisions on the same OAM mode are *resolved by
// operator scoping at the transport layer*: a Transient on operator X's
// wavelength is decoded with X's active schema for that OAM.
type ApplicationSchema struct {
	// ID is a registry-unique identifier (UUID v4).
	ID string `json:"id"`
	// OamMode is the topological charge ℓ this schema applies to.
	// Range matches otap-core's OamMode (-16..=+16 in the Rust impl).
	OamMode int32 `json:"oam_mode"`
	// PayloadBytes is the total on-wire payload length.
	// Must equal Layout.TotalBytes(); duplicated for fast wire-format checks.
	PayloadBytes int32 `json:"payload_bytes"`
	// Name is human-readable (e.g., "EquityTradeOrder", "MarketTick").
	Name string `json:"name"`
	// Version is a schema version number; the (OperatorID, OamMode, Version)
	// tuple uniquely identifies a schema across time.
	Version int32 `json:"version"`
	// Layout is the field-level decomposition of the payload.
	Layout *FieldLayout `json:"layout"`
	// OperatorID owns this schema; only this operator may publish updates.
	OperatorID string `json:"operator_id"`
	// Status indicates lifecycle state.
	Status SchemaStatus `json:"status"`
	// CreatedAt is the UTC time this schema record was created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the UTC time this schema record was last mutated.
	UpdatedAt time.Time `json:"updated_at"`
	// Signature is the ECDSA P-256 signature over the canonical schema bytes,
	// produced by the OperatorID's private key. Verifiers re-derive the
	// canonical bytes and check this signature against the operator's pubkey
	// in the operator registry.
	Signature []byte `json:"signature,omitempty"`
}

// Validate runs all structural integrity checks on a schema.
// Does *not* verify the signature; that requires the operator public key
// and is handled in the crypto engine.
func (s *ApplicationSchema) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("schema ID cannot be empty")
	}
	if s.Name == "" {
		return fmt.Errorf("schema name cannot be empty")
	}
	if s.OperatorID == "" {
		return fmt.Errorf("operator ID cannot be empty")
	}
	if s.OamMode < -16 || s.OamMode > 16 {
		return fmt.Errorf("oam_mode %d outside supported range [-16, 16]", s.OamMode)
	}
	if s.PayloadBytes <= 0 {
		return fmt.Errorf("payload_bytes must be positive (got %d)", s.PayloadBytes)
	}
	if s.Layout == nil {
		return fmt.Errorf("layout cannot be nil")
	}
	if err := s.Layout.Validate(); err != nil {
		return fmt.Errorf("layout invalid: %w", err)
	}
	if s.Layout.TotalBytes() != s.PayloadBytes {
		return fmt.Errorf("payload_bytes (%d) != layout total (%d)",
			s.PayloadBytes, s.Layout.TotalBytes())
	}
	return nil
}

// ToKey returns the per-operator deduplication key (oam_mode + version).
// Used by federation diff logic to detect duplicate publications.
func (s *ApplicationSchema) ToKey() string {
	return fmt.Sprintf("%s:%d:%d", s.OperatorID, s.OamMode, s.Version)
}
