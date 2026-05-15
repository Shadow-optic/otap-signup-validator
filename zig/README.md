# Zig-driven compile-time codegen

This directory contains Zig sources that run at *dev time* to generate Rust
files in `crates/otap-luxcode/src/`. There is no runtime Zig dependency —
the committed Rust is what builds.

Why Zig is here at all:

1. **`luxcode.zig`** runs the full 10B/12B codebook construction at Zig
   comptime. If fewer than `REQUIRED_PAIRS` valid disparity pairs survive the
   five constraints, the file fails to compile (`@compileError`). If it
   compiles, the spec is *proven* — no runtime panic, no exit-code check.
2. **`gf1024.zig`** computes the GF(1024) `exp`/`log` arrays and the
   16-symbol Reed-Solomon generator polynomial at comptime. The Rust side
   pays zero initialization cost; the tables live in `.rodata`.

To regenerate the Rust output:

```
make zig-codegen
```

That runs both Zig programs and writes their stdout to the corresponding
files under `crates/otap-luxcode/src/`. If you change a constraint or the
field parameters, re-run and commit the regenerated Rust together with the
Zig change.

The CI gate should re-run `make zig-codegen` and `git diff --exit-code` to
catch drift between Zig and committed Rust.
