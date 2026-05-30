//go:build amd64 && avx512

package bitweave

// avx512Enabled is true on amd64 builds compiled with the `avx512` tag.
// When true, computeConflictMasks dispatches to the assembly functions declared
// in bitweave_avx512_stubs.go.
//
// In a production build you would additionally gate this on runtime CPUID
// detection (golang.org/x/sys/cpu cpu.X86.HasAVX512F) so a binary compiled
// with the tag still falls back gracefully on a CPU without AVX-512.
const avx512Enabled = true

// CheckConflictsAVX512 is a named convenience wrapper kept for API
// compatibility and benchmarks. It is identical to CheckConflictsBuf on this
// build.
func (idx *LeaseIndex) CheckConflictsAVX512(
	channelNum uint32,
	awgID, tenantID, awgPort uint16,
	proposedExclusive bool,
	excludeSlot int,
	buf []Conflict,
) []Conflict {
	return idx.CheckConflictsBuf(channelNum, awgID, tenantID, awgPort, proposedExclusive, excludeSlot, buf)
}
