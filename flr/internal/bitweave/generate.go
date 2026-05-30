package bitweave

// Run `go generate` to regenerate the AVX-512 assembly from the avo generator.
// Requires: go install github.com/mmcloughlin/avo@latest
//
//go:generate go run ./avo/gen.go -out bitweave_avx512.s
