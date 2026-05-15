//! Persistent Replay Cache
//!
//! An on-disk, append-only ring buffer of recent handshake transcript hashes.
//! Guarantees that a daemon restart does not reset replay protection.
//! Uses `subtle::ConstantTimeEq` to prevent timing side-channels.

use std::fs::{File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::Path;
use subtle::ConstantTimeEq;

const HASH_SIZE: usize = 32;

pub struct PersistentReplayCache {
    file: File,
    capacity: usize,
    current_idx: usize,
    in_memory_cache: Vec<[u8; HASH_SIZE]>,
}

impl PersistentReplayCache {
    /// Opens or creates a persistent replay cache.
    pub fn open<P: AsRef<Path>>(path: P, capacity: usize) -> std::io::Result<Self> {
        let mut file = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .open(path)?;

        let mut in_memory_cache = Vec::new();
        let mut buf = [0u8; HASH_SIZE];
        let mut loaded = 0;

        file.seek(SeekFrom::Start(0))?;
        while let Ok(bytes_read) = file.read(&mut buf) {
            if bytes_read == HASH_SIZE {
                in_memory_cache.push(buf);
                loaded += 1;
                if loaded >= capacity {
                    break;
                }
            } else {
                break;
            }
        }

        let current_idx = loaded % capacity;

        let expected_file_size = (capacity * HASH_SIZE) as u64;
        if file.metadata()?.len() < expected_file_size {
            file.set_len(expected_file_size)?;
        }

        Ok(Self {
            file,
            capacity,
            current_idx,
            in_memory_cache,
        })
    }

    /// Check if a transcript hash has been seen. If not, persist it.
    pub fn check_and_insert(
        &mut self,
        transcript: [u8; HASH_SIZE],
    ) -> Result<(), &'static str> {
        // Constant-time scan
        for prev in &self.in_memory_cache {
            if prev.ct_eq(&transcript).into() {
                return Err("ReplayDetected");
            }
        }

        // Insert into memory ring
        if self.in_memory_cache.len() < self.capacity {
            self.in_memory_cache.push(transcript);
        } else {
            self.in_memory_cache[self.current_idx] = transcript;
        }

        // Persist to disk
        let offset = (self.current_idx * HASH_SIZE) as u64;
        self.file
            .seek(SeekFrom::Start(offset))
            .map_err(|_| "Disk seek error")?;
        self.file
            .write_all(&transcript)
            .map_err(|_| "Disk write error")?;
        self.file.sync_data().map_err(|_| "Disk sync error")?;

        self.current_idx = (self.current_idx + 1) % self.capacity;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::env::temp_dir;

    #[test]
    fn persistent_ring_survives_restart() {
        let path = temp_dir().join("otap_d3_replay_test.db");
        let _ = std::fs::remove_file(&path);

        let h1 = [1u8; 32];
        let h2 = [2u8; 32];
        let h3 = [3u8; 32];

        // Process 1
        {
            let mut cache = PersistentReplayCache::open(&path, 2).unwrap();
            assert!(cache.check_and_insert(h1).is_ok());
            assert!(cache.check_and_insert(h2).is_ok());
            assert!(cache.check_and_insert(h1).is_err()); // replay
            assert!(cache.check_and_insert(h3).is_ok()); // evicts h1
        }

        // Process 2 (restart)
        {
            let mut cache = PersistentReplayCache::open(&path, 2).unwrap();
            assert!(cache.check_and_insert(h2).is_err()); // survived
            assert!(cache.check_and_insert(h3).is_err()); // survived
            assert!(cache.check_and_insert(h1).is_ok()); // was evicted
        }

        let _ = std::fs::remove_file(&path);
    }
}
