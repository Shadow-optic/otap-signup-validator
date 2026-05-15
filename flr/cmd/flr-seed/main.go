// flr-seed: a small CLI that seeds an FLR instance with operators, endpoints,
// leases, and ApplicationSchemas needed by the Rust-side OBG demo. Useful for
// bringing up the full stack from a clean state.
//
// Usage:
//
//	flr-seed -url http://localhost:8080 -operator op-alice
//
// Communicates with the FLR over the REST API gateway. Idempotent: re-runs
// against an already-seeded FLR are no-ops (errors on duplicate creation are
// suppressed).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/otap/flr/internal/models"
)

var (
	flagURL      = flag.String("url", "http://localhost:8080", "FLR base URL")
	flagOperator = flag.String("operator", "op-alice", "operator ID to seed schemas under")
)

func main() {
	flag.Parse()
	base := strings.TrimRight(*flagURL, "/")

	if err := seed(base, *flagOperator); err != nil {
		fmt.Fprintf(os.Stderr, "seed failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("seed complete")
}

func seed(base, operatorID string) error {
	fmt.Printf("seeding FLR at %s for operator %s\n", base, operatorID)

	schemas := []*models.ApplicationSchema{
		equityTradeOrderSchema(operatorID),
		marketTickSchema(operatorID),
		heartbeatSchema(operatorID),
	}

	for _, sch := range schemas {
		if err := postSchema(base, sch); err != nil {
			// Distinguish "already exists" from real errors.
			if strings.Contains(err.Error(), "already exists") ||
				strings.Contains(err.Error(), "already has active") {
				fmt.Printf("  schema %s (oam %d): already present, skipping\n", sch.Name, sch.OamMode)
				continue
			}
			return fmt.Errorf("schema %s: %w", sch.Name, err)
		}
		fmt.Printf("  schema %s (oam %d): registered\n", sch.Name, sch.OamMode)
	}
	return nil
}

func postSchema(base string, s *models.ApplicationSchema) error {
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	resp, err := http.Post(base+"/v1/schemas", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusCreated {
		return nil
	}
	out, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("status %d: %s", resp.StatusCode, string(out))
}

// equityTradeOrderSchema mirrors the otap-schema/equity_order.rs Rust definition.
// The 256-byte layout must match bit-for-bit.
func equityTradeOrderSchema(operatorID string) *models.ApplicationSchema {
	return &models.ApplicationSchema{
		OamMode:      1,
		PayloadBytes: 256,
		Name:         "EquityTradeOrder",
		Version:      1,
		OperatorID:   operatorID,
		Status:       models.SchemaStatusActive,
		CreatedAt:    time.Now().UTC(),
		Layout: &models.FieldLayout{
			Fields: []models.FieldDef{
				{Name: "symbol", Offset: 0, Length: 4, Type: models.FieldTypeAscii,
					Description: "4-char ticker, null-padded"},
				{Name: "side", Offset: 4, Length: 1, Type: models.FieldTypeEnumU8,
					Description: "0=BUY, 1=SELL"},
				{Name: "_pad0", Offset: 5, Length: 3, Type: models.FieldTypeBytes,
					Description: "reserved"},
				{Name: "quantity", Offset: 8, Length: 4, Type: models.FieldTypeU32},
				{Name: "_pad1", Offset: 12, Length: 4, Type: models.FieldTypeBytes},
				{Name: "price_cents", Offset: 16, Length: 8, Type: models.FieldTypeF64},
				{Name: "timestamp_ns", Offset: 24, Length: 8, Type: models.FieldTypeU64},
				{Name: "client_order_id", Offset: 32, Length: 8, Type: models.FieldTypeU64},
				{Name: "firm_uuid", Offset: 40, Length: 16, Type: models.FieldTypeBytes},
				{Name: "_reserved", Offset: 56, Length: 200, Type: models.FieldTypeBytes},
			},
		},
	}
}

// marketTickSchema mirrors the otap-schema/market_tick.rs Rust definition.
func marketTickSchema(operatorID string) *models.ApplicationSchema {
	return &models.ApplicationSchema{
		OamMode:      3,
		PayloadBytes: 64,
		Name:         "MarketTick",
		Version:      1,
		OperatorID:   operatorID,
		Status:       models.SchemaStatusActive,
		CreatedAt:    time.Now().UTC(),
		Layout: &models.FieldLayout{
			Fields: []models.FieldDef{
				{Name: "symbol", Offset: 0, Length: 8, Type: models.FieldTypeAscii},
				{Name: "bid_cents", Offset: 8, Length: 8, Type: models.FieldTypeF64},
				{Name: "ask_cents", Offset: 16, Length: 8, Type: models.FieldTypeF64},
				{Name: "last_cents", Offset: 24, Length: 8, Type: models.FieldTypeF64},
				{Name: "volume", Offset: 32, Length: 8, Type: models.FieldTypeU64},
				{Name: "timestamp_ns", Offset: 40, Length: 8, Type: models.FieldTypeU64},
				{Name: "_reserved", Offset: 48, Length: 16, Type: models.FieldTypeBytes},
			},
		},
	}
}

// heartbeatSchema mirrors the otap-schema/heartbeat.rs Rust definition.
func heartbeatSchema(operatorID string) *models.ApplicationSchema {
	return &models.ApplicationSchema{
		OamMode:      4,
		PayloadBytes: 32,
		Name:         "Heartbeat",
		Version:      1,
		OperatorID:   operatorID,
		Status:       models.SchemaStatusActive,
		CreatedAt:    time.Now().UTC(),
		Layout: &models.FieldLayout{
			Fields: []models.FieldDef{
				{Name: "node_id", Offset: 0, Length: 8, Type: models.FieldTypeU64},
				{Name: "clock_offset_ns", Offset: 8, Length: 8, Type: models.FieldTypeI64},
				{Name: "heartbeat_seq", Offset: 16, Length: 8, Type: models.FieldTypeU64},
				{Name: "_reserved", Offset: 24, Length: 8, Type: models.FieldTypeBytes},
			},
		},
	}
}
