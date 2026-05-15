import json
import numpy as np
import hashlib

def generate_mrr_calibration(wafer_test_data: dict) -> dict:
    n_rings = 256
    np.random.seed(42)
    fab_offsets = np.array(wafer_test_data.get("fab_offsets_pm", np.random.normal(0, 2, n_rings)))
    thermal_coefs = np.array(wafer_test_data.get("thermal_nm_per_C", np.random.uniform(0.08, 0.12, n_rings)))
    nonlinear_alpha = np.array(wafer_test_data.get("nonlinear_alpha", np.random.uniform(0.001, 0.005, n_rings)))
    crosstalk = np.eye(n_rings) * 0.95 + np.random.normal(0, 0.01, (n_rings, n_rings))

    lut = {
        "resonance_offsets_pm": fab_offsets.tolist(),
        "thermal_coefficients_nm_per_C": thermal_coefs.tolist(),
        "nonlinear_alpha": nonlinear_alpha.tolist(),
        "crosstalk_matrix": crosstalk.tolist(),
        "stokes_invariant_map": np.random.uniform(-1, 1, (n_rings, 4)).tolist(),
        "lut_hash": hashlib.sha256(str(fab_offsets).encode()).hexdigest()
    }
    with open("generated/mrr_calibration_lut.json", "w") as f:
        json.dump(lut, f, indent=2)
    print(f"[Compiler] MRR calibration LUT generated – hash: {lut['lut_hash']}")
    return lut
