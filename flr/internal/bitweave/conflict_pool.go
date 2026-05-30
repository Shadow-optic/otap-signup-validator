package bitweave

import "sync"

// ConflictPool provides zero-allocation conflict detection by recycling the
// backing arrays of result slices via sync.Pool.
//
// It pools *[]Conflict (pointer to slice) rather than []Conflict to avoid
// boxing the slice header on every Get/Put — sync.Pool stores `any`, and a
// bare []Conflict would allocate a slice-header box on each insertion.
//
// The pool correctly handles append-driven reallocation: Query() captures the
// possibly-grown slice back into the pooled pointer before returning it, so the
// pool settles to the high-water-mark capacity of the workload and stays
// alloc-free after warm-up.
type ConflictPool struct {
	pool sync.Pool
}

// NewConflictPool creates a pool whose buffers start with capacity initialCap.
// Choose initialCap to cover the common case (e.g. 16) so most queries never grow.
func NewConflictPool(initialCap int) *ConflictPool {
	return &ConflictPool{
		pool: sync.Pool{
			New: func() any {
				b := make([]Conflict, 0, initialCap)
				return &b
			},
		},
	}
}

// Query runs a conflict check using a pooled buffer, invokes fn with the results,
// then returns the (possibly grown) buffer to the pool.
//
// This callback form makes the lifetime explicit and safe: all reads of the
// conflict slice happen inside fn, strictly before the buffer is recycled.
// After fn returns, the slice must not be retained — its backing array may be
// reused by another goroutine.
func (cp *ConflictPool) Query(
	idx *LeaseIndex,
	channelNum uint32,
	awgID, tenantID, awgPort uint16,
	proposedExclusive bool,
	excludeSlot int,
	fn func(conflicts []Conflict),
) {
	bufPtr := cp.pool.Get().(*[]Conflict)

	conflicts := idx.CheckConflictsAVX512(channelNum, awgID, tenantID, awgPort, proposedExclusive, excludeSlot, *bufPtr)

	// CRITICAL: capture the (possibly reallocated) backing array back into the
	// pooled pointer. If append grew the slice, `conflicts` points to a new array;
	// without this write we'd recycle the stale small buffer and lose the growth,
	// defeating the pool under load.
	*bufPtr = conflicts

	fn(conflicts)

	cp.pool.Put(bufPtr)
}

// GetBuffer returns a pooled backing buffer for manual Get/capture/Put use.
// The caller is responsible for the capture-before-Put discipline:
//
//	bufPtr := pool.GetBuffer()
//	conflicts := idx.CheckConflictsAVX512(..., *bufPtr)
//	*bufPtr = conflicts          // capture growth
//	// ... use conflicts (must finish before PutBuffer) ...
//	pool.PutBuffer(bufPtr)
func (cp *ConflictPool) GetBuffer() *[]Conflict {
	return cp.pool.Get().(*[]Conflict)
}

// PutBuffer returns a buffer to the pool. The caller must have already written
// any grown slice back into *bufPtr.
func (cp *ConflictPool) PutBuffer(bufPtr *[]Conflict) {
	cp.pool.Put(bufPtr)
}
