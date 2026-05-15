use std::io;

pub struct CxlPool {
    _path: String,
    _pool_id: String,
    backing: Vec<u8>,
}

impl CxlPool {
    pub fn new(path: &str, size: usize, pool_id: &str) -> Result<Self, io::Error> {
        Ok(Self {
            _path: path.to_string(),
            _pool_id: pool_id.to_string(),
            backing: vec![0; size],
        })
    }

    pub fn as_mut_ptr(&mut self) -> *mut u8 {
        self.backing.as_mut_ptr()
    }
}
