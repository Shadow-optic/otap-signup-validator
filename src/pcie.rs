use std::fs;

pub fn tune_pcie_mps_mrrs(device_path: &str, mps: u16, mrrs: u16) -> Result<(), std::io::Error> {
    fs::write(format!("{}/config", device_path), &mps.to_le_bytes())?;
    fs::write(format!("{}/config", device_path), &mrrs.to_le_bytes())?;
    Ok(())
}

pub fn enable_gpudirect_p2p(gpu_device_id: usize, fpga_bar_addr: u64) -> Result<(), std::io::Error> {
    unsafe {
        cudarc::driver::cudaDeviceEnablePeerAccess(gpu_device_id as i32, 0)?;
    }
    Ok(())
}
