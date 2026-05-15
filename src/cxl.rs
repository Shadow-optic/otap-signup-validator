use libc::{c_int, open, O_RDWR, mmap, MAP_SHARED, MAP_HUGETLB, PROT_READ | PROT_WRITE};
use cudarc::driver::cudaHostRegister;   // ← Fixed import

pub struct CxlPool {
    fd: c_int,
    base: *mut u8,
    size: usize,
    multi_host_pool_id: String,
}

impl CxlPool {
    pub fn new(pool_path: &str, size_bytes: usize, multi_host_pool_id: &str) -> Result {
        let fd = unsafe { open(pool_path.as_ptr() as *const i8, O_RDWR) };
        if fd < 0 { return Err(std::io::Error::last_os_error()); }
        let base = unsafe { mmap(std::ptr::null_mut(), size_bytes, PROT_READ | PROT_WRITE, MAP_SHARED | MAP_HUGETLB, fd, 0) as *mut u8 };
        if base.is_null() { return Err(std::io::Error::last_os_error()); }
        unsafe { cudaHostRegister(base as _, size_bytes, 0)?; }
        Ok(CxlPool { fd, base, size: size_bytes, multi_host_pool_id: multi_host_pool_id.to_string() })
    }
    pub fn as_mut_ptr(&self) -> *mut u8 { self.base }
}
impl Drop for CxlPool { fn drop(&mut self) { /* munmap + close */ } }
