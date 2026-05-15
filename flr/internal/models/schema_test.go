package models

import (
	"strings"
	"testing"
)

func validEquityLayout() *FieldLayout {
	return &FieldLayout{
		Fields: []FieldDef{
			{Name: "symbol", Offset: 0, Length: 4, Type: FieldTypeAscii},
			{Name: "side", Offset: 4, Length: 1, Type: FieldTypeEnumU8},
			{Name: "_pad0", Offset: 5, Length: 3, Type: FieldTypeBytes},
			{Name: "quantity", Offset: 8, Length: 4, Type: FieldTypeU32},
			{Name: "_pad1", Offset: 12, Length: 4, Type: FieldTypeBytes},
			{Name: "price_cents", Offset: 16, Length: 8, Type: FieldTypeF64},
			{Name: "timestamp_ns", Offset: 24, Length: 8, Type: FieldTypeU64},
			{Name: "client_order_id", Offset: 32, Length: 8, Type: FieldTypeU64},
			{Name: "firm_uuid", Offset: 40, Length: 16, Type: FieldTypeBytes},
			{Name: "_reserved", Offset: 56, Length: 200, Type: FieldTypeBytes},
		},
	}
}

func TestFieldLayout_Total(t *testing.T) {
	l := validEquityLayout()
	if got := l.TotalBytes(); got != 256 {
		t.Errorf("expected 256-byte layout, got %d", got)
	}
}

func TestFieldLayout_ValidatesValidLayout(t *testing.T) {
	l := validEquityLayout()
	if err := l.Validate(); err != nil {
		t.Fatalf("valid layout rejected: %v", err)
	}
}

func TestFieldLayout_RejectsOverlap(t *testing.T) {
	l := &FieldLayout{
		Fields: []FieldDef{
			{Name: "a", Offset: 0, Length: 4, Type: FieldTypeU32},
			{Name: "b", Offset: 2, Length: 4, Type: FieldTypeU32}, // overlap
		},
	}
	err := l.Validate()
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("expected overlap error, got: %v", err)
	}
}

func TestFieldLayout_RejectsGaps(t *testing.T) {
	l := &FieldLayout{
		Fields: []FieldDef{
			{Name: "a", Offset: 0, Length: 4, Type: FieldTypeU32},
			{Name: "b", Offset: 8, Length: 4, Type: FieldTypeU32}, // gap at 4..8
		},
	}
	err := l.Validate()
	if err == nil || !strings.Contains(err.Error(), "not covered") {
		t.Fatalf("expected gap error, got: %v", err)
	}
}

func TestFieldLayout_RejectsLengthMismatch(t *testing.T) {
	l := &FieldLayout{
		Fields: []FieldDef{
			// u32 declared with length 2 — invalid
			{Name: "a", Offset: 0, Length: 2, Type: FieldTypeU32},
		},
	}
	err := l.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires length 4") {
		t.Fatalf("expected length-mismatch error, got: %v", err)
	}
}

func TestApplicationSchema_ValidatesEquityOrder(t *testing.T) {
	sch := &ApplicationSchema{
		ID:           "00000000-0000-0000-0000-000000000001",
		OamMode:      1,
		PayloadBytes: 256,
		Name:         "EquityTradeOrder",
		Version:      1,
		Layout:       validEquityLayout(),
		OperatorID:   "op-alice",
		Status:       SchemaStatusActive,
	}
	if err := sch.Validate(); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

func TestApplicationSchema_RejectsOamOutOfRange(t *testing.T) {
	sch := &ApplicationSchema{
		ID: "x", OamMode: 99, PayloadBytes: 4, Name: "x", OperatorID: "y",
		Layout: &FieldLayout{Fields: []FieldDef{
			{Name: "x", Offset: 0, Length: 4, Type: FieldTypeU32},
		}},
	}
	err := sch.Validate()
	if err == nil || !strings.Contains(err.Error(), "oam_mode") {
		t.Fatalf("expected OAM error, got: %v", err)
	}
}

func TestApplicationSchema_RejectsPayloadMismatch(t *testing.T) {
	sch := &ApplicationSchema{
		ID: "x", OamMode: 1, PayloadBytes: 32, Name: "x", OperatorID: "y",
		Layout: &FieldLayout{Fields: []FieldDef{
			{Name: "a", Offset: 0, Length: 8, Type: FieldTypeU64},
		}},
	}
	err := sch.Validate()
	if err == nil || !strings.Contains(err.Error(), "layout total") {
		t.Fatalf("expected payload mismatch error, got: %v", err)
	}
}
