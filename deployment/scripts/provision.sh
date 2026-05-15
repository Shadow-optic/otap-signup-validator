#!/bin/bash
set -e
echo "OTAP 20 Tbps Provisioning Started"
python /opt/otap/compiler/mrr_calibration_generator.py --wafer-data /opt/otap/compiler/wafer_test_data.json
python /opt/otap/compiler/bitstream_compiler.py --lambda OTAP-L-CHI-NYC-042
photonforge_flash --config generated/photonforge_config.json
systemctl restart terabit_fabric
echo "Provisioning complete – 20 Tbps endpoint online"
