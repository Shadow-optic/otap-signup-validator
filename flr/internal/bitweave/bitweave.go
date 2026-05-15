// Package bitweave provides a branchless, SIMD-friendly conflict detection engine
// for the OTAP Federated Lambda Registry. It transposes lease attributes into
// bit-plane columns and evaluates conflict predicates across all active leases
// simultaneously using bitwise AND/ANDNOT operations.
//
// Integration: replaces the linear scan in registry.Engine.CheckConflict()
// and federation.conflict.DetectConflicts().
package bitweave

import (
	"fmt"
	"math/bits"
	"time"
)

const MaxLeases = 1024
const wordsPerPlane = MaxLeases / 64

type LeaseIndex struct {
	ChannelNum [32][wordsPerPlane]uint64
	AwgID      [16][wordsPerPlane]uint64
	TenantID   [16][wordsPerPlane]uint64
	AwgPort    [16][wordsPerPlane]uint64
	Active     [wordsPerPlane]uint64
	Exclusive  [wordsPerPlane]uint64
	LeaseIDs   [MaxLeases]string
	Count      int
}

func NewLeaseIndex() *LeaseIndex {
	return &LeaseIndex{}
}

func (idx *LeaseIndex) Insert(leaseID string, channelNum uint32, awgID, tenantID, awgPort uint16, exclusive bool) (int, error) {
	if idx.Count >= MaxLeases {
		return 0, fmt.Errorf("lease index full (%d max)", MaxLeases)
	}

	slot := idx.Count
	word := slot / 64
	bit := uint(slot % 64)

	idx.Active[word] |= 1 << bit
	if exclusive {
		idx.Exclusive[word] |= 1 << bit
	}

	for p := 0; p < 32; p++ {
		if (channelNum>>uint(p))&1 == 1 {
			idx.ChannelNum[p][word] |= 1 << bit
		}
	}
	for p := 0; p < 16; p++ {
		if (awgID>>uint(p))&1 == 1 {
			idx.AwgID[p][word] |= 1 << bit
		}
		if (tenantID>>uint(p))&1 == 1 {
			idx.TenantID[p][word] |= 1 << bit
		}
		if (awgPort>>uint(p))&1 == 1 {
			idx.AwgPort[p][word] |= 1 << bit
		}
	}

	idx.LeaseIDs[slot] = leaseID
	idx.Count++
	return slot, nil
}

func (idx *LeaseIndex) Remove(slot int) {
	if slot < 0 || slot >= MaxLeases {
		return
	}

	word := slot / 64
	mask := ^(uint64(1) << uint(slot%64))

	idx.Active[word] &= mask
	idx.Exclusive[word] &= mask
	for p := 0; p < 32; p++ {
		idx.ChannelNum[p][word] &= mask
	}
	for p := 0; p < 16; p++ {
		idx.AwgID[p][word] &= mask
		idx.TenantID[p][word] &= mask
		idx.AwgPort[p][word] &= mask
	}
	idx.LeaseIDs[slot] = ""
}

type ConflictType int

const (
	NoConflict       ConflictType = 0
	LambdaCollision  ConflictType = 1
	AwgPortCollision ConflictType = 2
	TenantIsolation  ConflictType = 3
)

type Conflict struct {
	Type      ConflictType
	LeaseID   string
	SlotIndex int
}

func (idx *LeaseIndex) CheckConflicts(channelNum uint32, awgID, tenantID, awgPort uint16, proposedExclusive bool, excludeSlot int) []Conflict {
	words := (idx.Count + 63) / 64
	if words == 0 {
		return nil
	}

	var channelMatch [wordsPerPlane]uint64
	copy(channelMatch[:], idx.Active[:])
	for p := 0; p < 32; p++ {
		bitSet := (channelNum >> uint(p)) & 1
		for w := 0; w < words; w++ {
			if bitSet == 1 {
				channelMatch[w] &= idx.ChannelNum[p][w]
			} else {
				channelMatch[w] &= ^idx.ChannelNum[p][w]
			}
		}
	}

	var awgMatch [wordsPerPlane]uint64
	copy(awgMatch[:], idx.Active[:])
	for p := 0; p < 16; p++ {
		bitSet := (awgID >> uint(p)) & 1
		for w := 0; w < words; w++ {
			if bitSet == 1 {
				awgMatch[w] &= idx.AwgID[p][w]
			} else {
				awgMatch[w] &= ^idx.AwgID[p][w]
			}
		}
	}

	var portMatch [wordsPerPlane]uint64
	copy(portMatch[:], idx.Active[:])
	for p := 0; p < 16; p++ {
		bitSet := (awgPort >> uint(p)) & 1
		for w := 0; w < words; w++ {
			if bitSet == 1 {
				portMatch[w] &= idx.AwgPort[p][w]
			} else {
				portMatch[w] &= ^idx.AwgPort[p][w]
			}
		}
	}

	var tenantMatch [wordsPerPlane]uint64
	copy(tenantMatch[:], idx.Active[:])
	for p := 0; p < 16; p++ {
		bitSet := (tenantID >> uint(p)) & 1
		for w := 0; w < words; w++ {
			if bitSet == 1 {
				tenantMatch[w] &= idx.TenantID[p][w]
			} else {
				tenantMatch[w] &= ^idx.TenantID[p][w]
			}
		}
	}
	var differentTenant [wordsPerPlane]uint64
	for w := 0; w < words; w++ {
		differentTenant[w] = idx.Active[w] & ^tenantMatch[w]
	}

	var lambdaConflicts [wordsPerPlane]uint64
	var proposedExclMask uint64
	if proposedExclusive {
		proposedExclMask = ^uint64(0)
	}
	for w := 0; w < words; w++ {
		sameChannelSameAwg := channelMatch[w] & awgMatch[w]
		lambdaConflicts[w] = sameChannelSameAwg & (idx.Exclusive[w] | proposedExclMask)
	}

	var portConflicts [wordsPerPlane]uint64
	for w := 0; w < words; w++ {
		portConflicts[w] = awgMatch[w] & portMatch[w]
	}

	var tenantConflicts [wordsPerPlane]uint64
	for w := 0; w < words; w++ {
		sameChannelSameAwg := channelMatch[w] & awgMatch[w]
		tenantConflicts[w] = sameChannelSameAwg & differentTenant[w]
	}

	if excludeSlot >= 0 && excludeSlot < MaxLeases {
		exWord := excludeSlot / 64
		exMask := ^(uint64(1) << uint(excludeSlot%64))
		lambdaConflicts[exWord] &= exMask
		portConflicts[exWord] &= exMask
		tenantConflicts[exWord] &= exMask
	}

	var results []Conflict
	for w := 0; w < words; w++ {
		b := lambdaConflicts[w]
		for b != 0 {
			trail := bits.TrailingZeros64(b)
			slot := w*64 + trail
			results = append(results, Conflict{Type: LambdaCollision, LeaseID: idx.LeaseIDs[slot], SlotIndex: slot})
			b &= b - 1
		}
		b = portConflicts[w]
		for b != 0 {
			trail := bits.TrailingZeros64(b)
			slot := w*64 + trail
			results = append(results, Conflict{Type: AwgPortCollision, LeaseID: idx.LeaseIDs[slot], SlotIndex: slot})
			b &= b - 1
		}
		b = tenantConflicts[w]
		for b != 0 {
			trail := bits.TrailingZeros64(b)
			slot := w*64 + trail
			results = append(results, Conflict{Type: TenantIsolation, LeaseID: idx.LeaseIDs[slot], SlotIndex: slot})
			b &= b - 1
		}
	}
	return results
}

type BenchmarkReport struct {
	LeaseCount     int
	QueriesRun     int
	TotalTime      time.Duration
	AvgQueryNs     float64
	ConflictsFound int
}

func RunBenchmark(n, q int) BenchmarkReport {
	idx := NewLeaseIndex()
	for i := 0; i < n && i < MaxLeases; i++ {
		idx.Insert(fmt.Sprintf("lease-%06d", i), uint32(i%176), uint16(i/176), uint16(i%10), uint16(i%176), true)
	}
	totalConflicts := 0
	start := time.Now()
	for i := 0; i < q; i++ {
		ch := uint32(i % 176)
		awg := uint16((i / 176) % 8)
		tenant := uint16((i + 3) % 10)
		port := uint16(ch)
		conflicts := idx.CheckConflicts(ch, awg, tenant, port, true, -1)
		totalConflicts += len(conflicts)
	}
	elapsed := time.Since(start)
	return BenchmarkReport{LeaseCount: n, QueriesRun: q, TotalTime: elapsed, AvgQueryNs: float64(elapsed.Nanoseconds()) / float64(q), ConflictsFound: totalConflicts}
}

// LensSnapshot represents a human-readable, reverse-transposed lease.
type LensSnapshot struct {
	LeaseID   string
	Channel   uint32
	AwgID     uint16
	TenantID  uint16
	AwgPort   uint16
	Exclusive bool
	Slot      int
}

// Lens reverse-transposes the bit-woven registry into human-readable structs.
// This is the COLD path — only called for debugging/observability, never in
// the conflict detection hot path. The hot-path bit-planes remain cache-optimal.
func (idx *LeaseIndex) Lens() []LensSnapshot {
	var snapshots []LensSnapshot
	words := (idx.Count + 63) / 64

	for w := 0; w < words; w++ {
		activeBits := idx.Active[w]

		for activeBits != 0 {
			trail := bits.TrailingZeros64(activeBits)
			slot := w*64 + trail

			// FIX: uint64(1) << uint(trail) prevents 32-bit truncation
			mask := uint64(1) << uint(trail)

			var channel uint32
			for p := 0; p < 32; p++ {
				if (idx.ChannelNum[p][w] & mask) != 0 {
					channel |= (1 << uint(p))
				}
			}

			var awgID uint16
			for p := 0; p < 16; p++ {
				if (idx.AwgID[p][w] & mask) != 0 {
					awgID |= (1 << uint(p))
				}
			}

			var tenantID uint16
			for p := 0; p < 16; p++ {
				if (idx.TenantID[p][w] & mask) != 0 {
					tenantID |= (1 << uint(p))
				}
			}

			var awgPort uint16
			for p := 0; p < 16; p++ {
				if (idx.AwgPort[p][w] & mask) != 0 {
					awgPort |= (1 << uint(p))
				}
			}

			exclusive := (idx.Exclusive[w] & mask) != 0

			snapshots = append(snapshots, LensSnapshot{
				LeaseID:   idx.LeaseIDs[slot],
				Channel:   channel,
				AwgID:     awgID,
				TenantID:  tenantID,
				AwgPort:   awgPort,
				Exclusive: exclusive,
				Slot:      slot,
			})

			activeBits &= activeBits - 1
		}
	}

	return snapshots
}
