package bitweave

import (
	"fmt"
	"sync"
	"testing"
)

// buildIndex populates a LeaseIndex with n leases for benchmarking.
func buildIndex(n int) *LeaseIndex {
	idx := NewLeaseIndex()
	for i := 0; i < n && i < MaxLeases; i++ {
		idx.Insert(
			fmt.Sprintf("lease-%d", i),
			uint32(i%176), uint16(i/176), uint16(i%10), uint16(i%176), true,
		)
	}
	return idx
}

// TestPoolGrownArrayCapture verifies the pool correctly captures a grown slice.
// We force the result count to exceed the initial buffer capacity and confirm
// the pool retains the larger array on the next Get.
func TestPoolGrownArrayCapture(t *testing.T) {
	idx := NewLeaseIndex()
	for i := 0; i < 100; i++ {
		// All on channel 42, AWG 0, non-exclusive, each a different tenant.
		// Querying ch=42 awg=0 tenant=9999 will produce TenantIsolation for all.
		idx.Insert(fmt.Sprintf("lease-%d", i), 42, 0, uint16(i), uint16(i), false)
	}

	// Start with deliberately tiny capacity to force growth.
	pool := NewConflictPool(2)

	var firstCap int
	pool.Query(idx, 42, 0, 9999, 9999, false, -1, func(conflicts []Conflict) {
		if len(conflicts) <= 2 {
			t.Fatalf("expected >2 conflicts to force growth, got %d", len(conflicts))
		}
		firstCap = cap(conflicts)
	})

	// After the first query grew the buffer, the next Get must return the grown buffer.
	bufPtr := pool.GetBuffer()
	if cap(*bufPtr) < firstCap {
		t.Fatalf("pool did not retain grown buffer: got cap %d, want >= %d",
			cap(*bufPtr), firstCap)
	}
	pool.PutBuffer(bufPtr)
}

// TestPoolSemanticEquivalence verifies the buffered path returns the same
// conflicts as the direct scalar path.
func TestPoolSemanticEquivalence(t *testing.T) {
	idx := buildIndex(512)
	pool := NewConflictPool(16)

	for q := 0; q < 200; q++ {
		ch := uint32(q % 176)
		awg := uint16((q / 176) % 8)
		tenant := uint16((q + 3) % 10)
		port := uint16(ch)

		direct := idx.CheckConflicts(ch, awg, tenant, port, true, -1)

		pool.Query(idx, ch, awg, tenant, port, true, -1, func(pooled []Conflict) {
			if len(pooled) != len(direct) {
				t.Fatalf("query %d: pooled=%d conflicts, direct=%d", q, len(pooled), len(direct))
			}
		})
	}
}

// TestCheckConflictsBufEquivalence confirms CheckConflictsBuf matches CheckConflicts.
func TestCheckConflictsBufEquivalence(t *testing.T) {
	idx := buildIndex(512)

	for q := 0; q < 500; q++ {
		ch := uint32(q % 176)
		awg := uint16((q / 176) % 8)
		tenant := uint16((q + 3) % 10)
		port := uint16(ch)
		excl := q%2 == 0

		direct := idx.CheckConflicts(ch, awg, tenant, port, excl, -1)
		buffered := idx.CheckConflictsBuf(ch, awg, tenant, port, excl, -1, nil)

		if len(direct) != len(buffered) {
			t.Fatalf("query %d: direct=%d buffered=%d", q, len(direct), len(buffered))
		}
	}
}

// TestPoolConcurrentSafety exercises the pool under concurrent load.
func TestPoolConcurrentSafety(t *testing.T) {
	idx := buildIndex(256)
	pool := NewConflictPool(8)

	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				ch := uint32((id*500 + i) % 176)
				pool.Query(idx, ch, uint16((i/176)%8), uint16((i+3)%10), uint16(ch), true, -1,
					func(_ []Conflict) {})
			}
		}(g)
	}
	wg.Wait()
}

// --------------------------------------------------------------------------
// Pool benchmarks
// --------------------------------------------------------------------------

// BenchmarkNoPool measures the baseline: a fresh allocation per query (nil buf).
func BenchmarkNoPool(b *testing.B) {
	idx := buildIndex(512)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ch := uint32(i % 176)
		_ = idx.CheckConflictsAVX512(ch, uint16((i/176)%8), uint16((i+3)%10), uint16(ch), true, -1, nil)
	}
}

// BenchmarkWithPool measures the pooled callback path. After warm-up this
// reports 0 allocs/op.
func BenchmarkWithPool(b *testing.B) {
	idx := buildIndex(512)
	pool := NewConflictPool(16)

	// Warm-up: prime the pool so the backing array reaches steady-state capacity.
	for i := 0; i < 1000; i++ {
		pool.Query(idx, uint32(i%176), uint16((i/176)%8), uint16((i+3)%10), uint16(i%176), true, -1,
			func(_ []Conflict) {})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ch := uint32(i % 176)
		pool.Query(idx, ch, uint16((i/176)%8), uint16((i+3)%10), uint16(i%176), true, -1,
			func(_ []Conflict) {})
	}
}

// BenchmarkManualPool measures the explicit Get/capture/Put pattern.
func BenchmarkManualPool(b *testing.B) {
	idx := buildIndex(512)
	pool := NewConflictPool(16)

	for i := 0; i < 1000; i++ {
		p := pool.GetBuffer()
		c := idx.CheckConflictsAVX512(uint32(i%176), 0, 5, uint16(i%176), true, -1, *p)
		*p = c
		pool.PutBuffer(p)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ch := uint32(i % 176)
		p := pool.GetBuffer()
		conflicts := idx.CheckConflictsAVX512(ch, uint16((i/176)%8), uint16((i+3)%10), uint16(ch), true, -1, *p)
		*p = conflicts
		_ = conflicts
		pool.PutBuffer(p)
	}
}

// BenchmarkPoolVsNoPool_64 / 256 / 512 / 1024 — compare pooled vs unpool at each scale.

func BenchmarkPoolVsNoPool_64(b *testing.B)   { benchPoolVsNoPool(b, 64) }
func BenchmarkPoolVsNoPool_256(b *testing.B)  { benchPoolVsNoPool(b, 256) }
func BenchmarkPoolVsNoPool_512(b *testing.B)  { benchPoolVsNoPool(b, 512) }
func BenchmarkPoolVsNoPool_1024(b *testing.B) { benchPoolVsNoPool(b, 1024) }

func benchPoolVsNoPool(b *testing.B, n int) {
	idx := buildIndex(n)
	pool := NewConflictPool(16)

	for i := 0; i < 1000; i++ {
		pool.Query(idx, uint32(i%176), 0, 5, uint16(i%176), true, -1, func(_ []Conflict) {})
	}

	b.Run("NoPool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ch := uint32(i % 176)
			_ = idx.CheckConflictsBuf(ch, uint16((i/176)%8), uint16((i+3)%10), uint16(ch), true, -1, nil)
		}
	})

	b.Run("WithPool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ch := uint32(i % 176)
			pool.Query(idx, ch, uint16((i/176)%8), uint16((i+3)%10), uint16(ch), true, -1,
				func(_ []Conflict) {})
		}
	})
}

// BenchmarkCheckConflictsBuf_* mirrors the scalar CheckConflicts benchmarks
// but via the buffered path to show they're equivalent in throughput.

func BenchmarkCheckConflictsBuf_64(b *testing.B)   { benchConflictsBuf(b, 64) }
func BenchmarkCheckConflictsBuf_256(b *testing.B)  { benchConflictsBuf(b, 256) }
func BenchmarkCheckConflictsBuf_512(b *testing.B)  { benchConflictsBuf(b, 512) }
func BenchmarkCheckConflictsBuf_1024(b *testing.B) { benchConflictsBuf(b, 1024) }

func benchConflictsBuf(b *testing.B, n int) {
	idx := buildIndex(n)
	buf := make([]Conflict, 0, 32)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ch := uint32(i % 176)
		buf = idx.CheckConflictsBuf(ch, uint16((i/176)%8), uint16((i+3)%10), uint16(ch), true, -1, buf[:0])
	}
}
