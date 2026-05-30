//go:build !amd64 || !avx512

package bitweave

// avx512Enabled is false unless the binary is built with the `avx512` tag on
// amd64. computeConflictMasks then uses the portable scalar bit-plane scan.
const avx512Enabled = false

// CheckConflictsAVX512 wrapper for API compatibility on non-avx512 builds.
// Routes through the same buffered scalar path.
func (idx *LeaseIndex) CheckConflictsAVX512(
	channelNum uint32,
	awgID, tenantID, awgPort uint16,
	proposedExclusive bool,
	excludeSlot int,
	buf []Conflict,
) []Conflict {
	return idx.CheckConflictsBuf(channelNum, awgID, tenantID, awgPort, proposedExclusive, excludeSlot, buf)
}

// Unreachable stubs so computeConflictMasks type-checks on non-avx512 builds.
// avx512Enabled is a compile-time false constant so these branches are
// eliminated by dead-code analysis and the bodies never execute.

func matchAttribute32AVX512(_ *[32][wordsPerPlane]uint64, _ *[wordsPerPlane]uint64, _ uint32, _ *[wordsPerPlane]uint64) {
	panic("avx512 not enabled")
}

func matchAttribute16AVX512(_ *[16][wordsPerPlane]uint64, _ *[wordsPerPlane]uint64, _ uint16, _ *[wordsPerPlane]uint64) {
	panic("avx512 not enabled")
}
