use std::io;

pub fn tune_pcie_mps_mrrs(_device_path: &str, _mps: u32, _mrrs: u32) -> Result<(), io::Error> {
    Ok(())
}

pub fn enable_gpudirect_p2p(_gpu_id: usize, _bar_addr: u64) -> Result<(), io::Error> {
    Ok(())
}
