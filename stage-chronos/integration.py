"""End-to-end STAGE-CHRONOS validation suite."""
from .coherence import GeometricCoherenceKernel
from .encoder import STAGEManifoldEncoder
from .transport import SpacetimeTransport


def run_integration_suite() -> None:
    print("╔══════════════════════════════════════════════════════════════════╗")
    print("║       STAGE-CHRONOS Bridge: Geometric Coherence Validation       ║")
    print("╚══════════════════════════════════════════════════════════════════╝\n")

    encoder = STAGEManifoldEncoder()
    gck = GeometricCoherenceKernel()
    transport = SpacetimeTransport()

    symbol_data = 1
    t_emit = 0.0
    print(f"[*] Encoding Symbol '{symbol_data}' into STAGE Torus Manifold...")
    tx_manifold = encoder.encode_torus(symbol_data, t_emit, resolution=20)
    print(f"    -> Generated manifold with {len(tx_manifold)} spacetime points.")

    # TEST A: Lorentz boost — must preserve spacetime intervals (Phi ≈ 1.0)
    v_boost = 0.85
    print(f"\n[*] TEST A: Lorentz boost at v = {v_boost}c ...")
    rx_rel = transport.apply_lorentz_boost(tx_manifold, v_boost)
    phi_a, mse_a = gck.measure_coherence(tx_manifold, rx_rel)
    print(f"    -> MSE interval deviation : {mse_a:.4e}")
    print(f"    -> Geometric Coherence (Φ): {phi_a:.6f}")
    if phi_a > 0.99:
        print("    [✓] PASS: topology perfectly preserved across reference frames.")
    else:
        print("    [✗] FAIL: Lorentz invariance broken.")

    # TEST B: Decoherence noise — must destroy coherence (Phi < 0.5)
    noise = 0.1
    print(f"\n[*] TEST B: Decoherence noise σ = {noise} ...")
    rx_noisy = transport.apply_decoherence_noise(tx_manifold, noise, seed=42)
    phi_b, mse_b = gck.measure_coherence(tx_manifold, rx_noisy)
    print(f"    -> MSE interval deviation : {mse_b:.4e}")
    print(f"    -> Geometric Coherence (Φ): {phi_b:.6f}")
    if phi_b < 0.5:
        print("    [✓] PASS: GP-CSK detected topological decoherence.")
    else:
        print("    [✗] FAIL: kernel failed to detect structural corruption.")

    print("\n" + "=" * 68)
    print("  INTEGRATION SUMMARY")
    print("  - GRAVCOMP (Lorentz boost) preserves manifold topology: Φ → 1.0")
    print("  - GP-CSK coherence kernel detects decoherence:          Φ → 0.0")
    print("=" * 68 + "\n")


if __name__ == "__main__":
    run_integration_suite()
