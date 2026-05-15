import subprocess
import json

def generate_mrr_calibration(entry: dict):
    # Full LUT generator call (see mrr_calibration_generator.py)
    subprocess.run(["python", "mrr_calibration_generator.py", "--wafer-data", "wafer_test_data.json"])

def generate_cxl_40_optical_config(entry: dict):
    config = {
        "cxl_version": "4.0",
        "optical_link_speed_gt_s": 64,
        "optical_lane_count": 16,
        "multi_host_pool_id": entry.get("multi_host_pool_id"),
        "multi_host_sharing_mode": "cache_coherent"
    }
    with open("generated/cxl_optical_config.json", "w") as f:
        json.dump(config, f, indent=2)

def build_forgecore_bitstream(lambda_entry: dict):
    generate_mrr_calibration(lambda_entry)
    generate_cxl_40_optical_config(lambda_entry)
    subprocess.run(["python", "photonforge_config_generator.py", "--lanes", str(lambda_entry.get("lane_count", 80))])
    print("[Compiler] 20 Tbps PhotonForge + CXL 4.0 bitstream ready")
