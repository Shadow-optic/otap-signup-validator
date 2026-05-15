package bitweave

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestLambdaCollision(t *testing.T) {
	idx := NewLeaseIndex()
	idx.Insert("existing-lease", 42, 1, 100, 7, true)

	conflicts := idx.CheckConflicts(42, 1, 100, 8, false, -1)
	found := false
	for _, c := range conflicts {
		if c.Type == LambdaCollision && c.LeaseID == "existing-lease" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected LambdaCollision when existing is exclusive, got %v", conflicts)
	}
}

func TestProposedExclusiveTriggersConflict(t *testing.T) {
	idx := NewLeaseIndex()
	idx.Insert("shared-lease", 42, 1, 100, 7, false) // existing is NOT exclusive

	// proposedExclusive=true should still conflict
	conflicts := idx.CheckConflicts(42, 1, 100, 8, true, -1)
	found := false
	for _, c := range conflicts {
		if c.Type == LambdaCollision && c.LeaseID == "shared-lease" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected LambdaCollision when proposed is exclusive, got %v", conflicts)
	}
}

func TestNoConflictBothNonExclusive(t *testing.T) {
	idx := NewLeaseIndex()
	idx.Insert("shared-lease", 42, 1, 100, 7, false) // existing NOT exclusive

	// proposed NOT exclusive — no lambda conflict (but tenant conflict if different tenant)
	conflicts := idx.CheckConflicts(42, 1, 100, 8, false, -1)
	for _, c := range conflicts {
		if c.Type == LambdaCollision {
			t.Errorf("unexpected LambdaCollision when neither side is exclusive: %v", c)
		}
	}
}

func TestNoConflictDifferentChannel(t *testing.T) {
	idx := NewLeaseIndex()
	idx.Insert("lease-a", 42, 1, 100, 7, true)
	conflicts := idx.CheckConflicts(43, 1, 100, 8, true, -1)

	for _, c := range conflicts {
		if c.Type == LambdaCollision {
			t.Errorf("unexpected LambdaCollision on different channel: %v", c)
		}
	}
}

func TestNoConflictDifferentAwg(t *testing.T) {
	idx := NewLeaseIndex()
	idx.Insert("lease-a", 42, 1, 100, 7, true)
	conflicts := idx.CheckConflicts(42, 2, 100, 7, true, -1)

	for _, c := range conflicts {
		if c.Type == LambdaCollision {
			t.Errorf("unexpected LambdaCollision on different AWG: %v", c)
		}
	}
}

func TestAwgPortCollision(t *testing.T) {
	idx := NewLeaseIndex()
	idx.Insert("lease-port7", 42, 1, 100, 7, true)
	conflicts := idx.CheckConflicts(99, 1, 100, 7, true, -1)

	found := false
	for _, c := range conflicts {
		if c.Type == AwgPortCollision && c.LeaseID == "lease-port7" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AwgPortCollision, got %v", conflicts)
	}
}

func TestTenantIsolation(t *testing.T) {
	idx := NewLeaseIndex()
	idx.Insert("tenant-a-lease", 42, 1, 100, 7, false)
	conflicts := idx.CheckConflicts(42, 1, 200, 8, false, -1)

	found := false
	for _, c := range conflicts {
		if c.Type == TenantIsolation && c.LeaseID == "tenant-a-lease" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected TenantIsolation, got %v", conflicts)
	}
}

func TestSelfExclusion(t *testing.T) {
	idx := NewLeaseIndex()
	slot, _ := idx.Insert("my-lease", 42, 1, 100, 7, true)
	conflicts := idx.CheckConflicts(42, 1, 100, 7, true, slot)

	for _, c := range conflicts {
		if c.LeaseID == "my-lease" {
			t.Errorf("self-exclusion failed — found conflict with own lease: %v", c)
		}
	}
}

func TestRemoveLease(t *testing.T) {
	idx := NewLeaseIndex()
	slot, _ := idx.Insert("to-remove", 42, 1, 100, 7, true)
	conflicts := idx.CheckConflicts(42, 1, 100, 8, true, -1)
	if len(conflicts) == 0 {
		t.Fatal("expected conflict before removal")
	}

	idx.Remove(slot)
	conflicts = idx.CheckConflicts(42, 1, 100, 8, true, -1)
	for _, c := range conflicts {
		if c.LeaseID == "to-remove" {
			t.Errorf("removed lease still appears in conflicts: %v", c)
		}
	}
}

// --------------------------------------------------------------------------
// Property-based fuzzer: validates parallel engine against O(n) linear scan
// --------------------------------------------------------------------------

type ReferenceLease struct {
	ID        string
	Channel   uint32
	AwgID     uint16
	TenantID  uint16
	AwgPort   uint16
	Exclusive bool
	Slot      int
}

func TestExhaustiveBitweaveFuzz(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	idx := NewLeaseIndex()
	var truth []ReferenceLease

	for i := 0; i < MaxLeases; i++ {
		lease := ReferenceLease{
			ID:        fmt.Sprintf("fuzz-%d", i),
			Channel:   uint32(r.Intn(10)),
			AwgID:     uint16(r.Intn(3)),
			TenantID:  uint16(r.Intn(5)),
			AwgPort:   uint16(r.Intn(10)),
			Exclusive: r.Intn(2) == 1,
			Slot:      i,
		}
		truth = append(truth, lease)
		idx.Insert(lease.ID, lease.Channel, lease.AwgID, lease.TenantID, lease.AwgPort, lease.Exclusive)
	}

	for q := 0; q < 10000; q++ {
		qCh := uint32(r.Intn(10))
		qAwg := uint16(r.Intn(3))
		qTen := uint16(r.Intn(5))
		qPort := uint16(r.Intn(10))
		qExcl := r.Intn(2) == 1

		fastConflicts := idx.CheckConflicts(qCh, qAwg, qTen, qPort, qExcl, -1)

		var slowConflicts []Conflict
		for _, ref := range truth {
			sameCh := ref.Channel == qCh
			sameAwg := ref.AwgID == qAwg
			sameTen := ref.TenantID == qTen
			samePort := ref.AwgPort == qPort

			if sameCh && sameAwg && (ref.Exclusive || qExcl) {
				slowConflicts = append(slowConflicts, Conflict{Type: LambdaCollision, LeaseID: ref.ID, SlotIndex: ref.Slot})
			}
			if sameAwg && samePort {
				slowConflicts = append(slowConflicts, Conflict{Type: AwgPortCollision, LeaseID: ref.ID, SlotIndex: ref.Slot})
			}
			if sameCh && sameAwg && !sameTen {
				slowConflicts = append(slowConflicts, Conflict{Type: TenantIsolation, LeaseID: ref.ID, SlotIndex: ref.Slot})
			}
		}

		if len(fastConflicts) != len(slowConflicts) {
			t.Fatalf("Mismatch on query %d! Fast=%d Slow=%d Query: Ch=%d Awg=%d Ten=%d Port=%d Excl=%v",
				q, len(fastConflicts), len(slowConflicts), qCh, qAwg, qTen, qPort, qExcl)
		}

		sortFn := func(arr []Conflict) func(i, j int) bool {
			return func(i, j int) bool {
				if arr[i].SlotIndex == arr[j].SlotIndex {
					return arr[i].Type < arr[j].Type
				}
				return arr[i].SlotIndex < arr[j].SlotIndex
			}
		}
		sort.Slice(fastConflicts, sortFn(fastConflicts))
		sort.Slice(slowConflicts, sortFn(slowConflicts))

		for i := range fastConflicts {
			if fastConflicts[i] != slowConflicts[i] {
				t.Fatalf("Conflict mismatch at index %d!\nFast: %+v\nSlow: %+v", i, fastConflicts[i], slowConflicts[i])
			}
		}
	}
}

// --------------------------------------------------------------------------
// Benchmarks
// --------------------------------------------------------------------------

func benchConflicts(b *testing.B, n int) {
	idx := NewLeaseIndex()
	for i := 0; i < n; i++ {
		idx.Insert(fmt.Sprintf("lease-%d", i), uint32(i%176), uint16(i/176), uint16(i%10), uint16(i%176), true)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ch := uint32(i % 176)
		awg := uint16((i / 176) % 8)
		tenant := uint16((i + 3) % 10)
		port := uint16(ch)
		_ = idx.CheckConflicts(ch, awg, tenant, port, true, -1)
	}
}

func BenchmarkCheckConflicts_64Leases(b *testing.B)   { benchConflicts(b, 64) }
func BenchmarkCheckConflicts_256Leases(b *testing.B)  { benchConflicts(b, 256) }
func BenchmarkCheckConflicts_512Leases(b *testing.B)  { benchConflicts(b, 512) }
func BenchmarkCheckConflicts_1024Leases(b *testing.B) { benchConflicts(b, 1024) }

// --------------------------------------------------------------------------
// Lens reverse-transposition validation
// --------------------------------------------------------------------------

func TestLensReverseTranspositionE2E(t *testing.T) {
	idx := NewLeaseIndex()
	type expected struct {
		channel   uint32
		awgID     uint16
		tenantID  uint16
		awgPort   uint16
		exclusive bool
	}
	want := make(map[string]expected)

	cases := []struct {
		id        string
		ch        uint32
		awg       uint16
		tenant    uint16
		port      uint16
		exclusive bool
	}{
		{"lens-zero", 0, 0, 0, 0, false},
		{"lens-max", 0xFFFFFFFF, 0xFFFF, 0xFFFF, 0xFFFF, true},
		{"lens-mid", 42, 7, 99, 12, true},
		{"lens-remove-me", 1024, 2, 1, 8, false},
	}

	for _, c := range cases {
		slot, _ := idx.Insert(c.id, c.ch, c.awg, c.tenant, c.port, c.exclusive)
		want[c.id] = expected{c.ch, c.awg, c.tenant, c.port, c.exclusive}
		_ = slot
	}

	// Remove one to verify Active mask respects deletion
	idx.Remove(3)
	delete(want, "lens-remove-me")

	snapshots := idx.Lens()
	if len(snapshots) != len(want) {
		t.Fatalf("expected %d snapshots, got %d", len(want), len(snapshots))
	}

	for _, snap := range snapshots {
		exp, ok := want[snap.LeaseID]
		if !ok {
			t.Fatalf("Lens returned unexpected lease: %s", snap.LeaseID)
		}
		if snap.Channel != exp.channel || snap.AwgID != exp.awgID ||
			snap.TenantID != exp.tenantID || snap.AwgPort != exp.awgPort ||
			snap.Exclusive != exp.exclusive {
			t.Fatalf("Data corruption!\n  Lease: %s\n  Expected: ch=%d awg=%d ten=%d port=%d excl=%v\n  Got:      ch=%d awg=%d ten=%d port=%d excl=%v",
				snap.LeaseID,
				exp.channel, exp.awgID, exp.tenantID, exp.awgPort, exp.exclusive,
				snap.Channel, snap.AwgID, snap.TenantID, snap.AwgPort, snap.Exclusive)
		}
	}
}

// --------------------------------------------------------------------------
// Hot-path vs cold-path benchmark (with corrected 6-arg CheckConflicts call)
// --------------------------------------------------------------------------

func BenchmarkHotPathVsLens(b *testing.B) {
	idx := NewLeaseIndex()
	for i := 0; i < 1024; i++ {
		idx.Insert(fmt.Sprintf("lease-%d", i), uint32(i), uint16(i%8), uint16(i%10), uint16(i), true)
	}

	b.Run("HotPath_ConflictCheck", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			// FIX: 6 args — proposedExclusive=true forces maximal Lambda Collision path
			_ = idx.CheckConflicts(42, 1, 3, 42, true, -1)
		}
	})

	b.Run("ColdPath_LensExtraction", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = idx.Lens()
		}
	})
}
