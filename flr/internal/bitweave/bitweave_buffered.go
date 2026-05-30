package bitweave

import "math/bits"

// CheckConflictsBuf is the zero-allocation entry point. It evaluates all
// conflict predicates and appends results into buf[:0], reusing the backing
// array. Pass nil for a fresh allocation, or a pooled slice for zero-alloc
// operation.
//
// On amd64 with the avx512 build tag, computeConflictMasks dispatches to the
// AVX-512 assembly path. Otherwise it uses the portable scalar bit-plane scan.
//
// CRITICAL for pooling: the returned slice may alias buf (fit) or be a newly
// grown array (overflow). Callers using sync.Pool must capture the return back
// into the pooled pointer before Put. Use ConflictPool.Query for the safe
// callback form.
func (idx *LeaseIndex) CheckConflictsBuf(
	channelNum uint32,
	awgID, tenantID, awgPort uint16,
	proposedExclusive bool,
	excludeSlot int,
	buf []Conflict,
) []Conflict {
	lambda, port, tenant := idx.computeConflictMasks(channelNum, awgID, tenantID, awgPort, proposedExclusive, excludeSlot)
	return idx.extractConflicts(lambda, port, tenant, buf)
}

// computeConflictMasks runs the bit-plane scan and returns the three conflict
// masks. On avx512 builds the per-attribute matching is accelerated; otherwise
// the portable scalar path is used. Both produce identical masks.
func (idx *LeaseIndex) computeConflictMasks(
	channelNum uint32,
	awgID, tenantID, awgPort uint16,
	proposedExclusive bool,
	excludeSlot int,
) (lambda, port, tenant [wordsPerPlane]uint64) {
	words := (idx.Count + 63) / 64
	if words == 0 {
		return
	}

	var channelMatch, awgMatch, portMatch, tenantMatch [wordsPerPlane]uint64

	if avx512Enabled {
		matchAttribute32AVX512(&idx.ChannelNum, &idx.Active, channelNum, &channelMatch)
		matchAttribute16AVX512(&idx.AwgID, &idx.Active, awgID, &awgMatch)
		matchAttribute16AVX512(&idx.AwgPort, &idx.Active, awgPort, &portMatch)
		matchAttribute16AVX512(&idx.TenantID, &idx.Active, tenantID, &tenantMatch)
	} else {
		copy(channelMatch[:], idx.Active[:])
		copy(awgMatch[:], idx.Active[:])
		copy(portMatch[:], idx.Active[:])
		copy(tenantMatch[:], idx.Active[:])

		matchScalar32(&channelMatch, &idx.ChannelNum, channelNum, words)
		matchScalar16(&awgMatch, &idx.AwgID, awgID, words)
		matchScalar16(&portMatch, &idx.AwgPort, awgPort, words)
		matchScalar16(&tenantMatch, &idx.TenantID, tenantID, words)
	}

	var proposedExclMask uint64
	if proposedExclusive {
		proposedExclMask = ^uint64(0)
	}

	var differentTenant [wordsPerPlane]uint64
	for w := 0; w < words; w++ {
		differentTenant[w] = idx.Active[w] & ^tenantMatch[w]
	}

	for w := 0; w < words; w++ {
		sameChAwg := channelMatch[w] & awgMatch[w]
		lambda[w] = sameChAwg & (idx.Exclusive[w] | proposedExclMask)
		port[w] = awgMatch[w] & portMatch[w]
		tenant[w] = sameChAwg & differentTenant[w]
	}

	if excludeSlot >= 0 && excludeSlot < MaxLeases {
		exWord := excludeSlot / 64
		exMask := ^(uint64(1) << uint(excludeSlot%64))
		lambda[exWord] &= exMask
		port[exWord] &= exMask
		tenant[exWord] &= exMask
	}
	return
}

func matchScalar32(match *[wordsPerPlane]uint64, planes *[32][wordsPerPlane]uint64, target uint32, words int) {
	for p := 0; p < 32; p++ {
		if (target>>uint(p))&1 == 1 {
			for w := 0; w < words; w++ {
				match[w] &= planes[p][w]
			}
		} else {
			for w := 0; w < words; w++ {
				match[w] &= ^planes[p][w]
			}
		}
	}
}

func matchScalar16(match *[wordsPerPlane]uint64, planes *[16][wordsPerPlane]uint64, target uint16, words int) {
	for p := 0; p < 16; p++ {
		if (target>>uint(p))&1 == 1 {
			for w := 0; w < words; w++ {
				match[w] &= planes[p][w]
			}
		} else {
			for w := 0; w < words; w++ {
				match[w] &= ^planes[p][w]
			}
		}
	}
}

// extractConflicts walks the three conflict masks and appends results into
// buf[:0], reusing the backing array. Shared by all platforms.
func (idx *LeaseIndex) extractConflicts(
	lambda, port, tenant [wordsPerPlane]uint64,
	buf []Conflict,
) []Conflict {
	words := (idx.Count + 63) / 64
	results := buf[:0]

	for w := 0; w < words; w++ {
		b := lambda[w]
		for b != 0 {
			trail := bits.TrailingZeros64(b)
			slot := w*64 + trail
			results = append(results, Conflict{LambdaCollision, idx.LeaseIDs[slot], slot})
			b &= b - 1
		}
		b = port[w]
		for b != 0 {
			trail := bits.TrailingZeros64(b)
			slot := w*64 + trail
			results = append(results, Conflict{AwgPortCollision, idx.LeaseIDs[slot], slot})
			b &= b - 1
		}
		b = tenant[w]
		for b != 0 {
			trail := bits.TrailingZeros64(b)
			slot := w*64 + trail
			results = append(results, Conflict{TenantIsolation, idx.LeaseIDs[slot], slot})
			b &= b - 1
		}
	}
	return results
}
