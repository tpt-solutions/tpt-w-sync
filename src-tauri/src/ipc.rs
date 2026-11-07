// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

//! Client for the daemon's control channel. The endpoint naming matches
//! engine/ipc in the Go module: a per-user named pipe on Windows and a
//! user-only Unix socket elsewhere. Protocol: one JSON request and one JSON
//! response per line.

use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};

#[derive(Debug, Deserialize)]
pub struct Response {
    pub ok: bool,
    #[serde(default)]
    pub error: Option<String>,
    #[serde(default)]
    pub data: Option<Value>,
}

#[derive(Serialize)]
struct Request<'a> {
    op: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    job: Option<&'a str>,
}

/// The config directory the daemon uses: <user config dir>/tpt-sync.
pub fn config_dir() -> Result<PathBuf, String> {
    dirs::config_dir()
        .map(|d| d.join("tpt-sync"))
        .ok_or_else(|| "no user configuration directory on this system".to_string())
}

/// Control endpoint for a config directory, identical to ipc.DefaultEndpoint.
pub fn endpoint(config_dir: &Path) -> String {
    if cfg!(windows) {
        let cleaned = config_dir.to_string_lossy().to_lowercase();
        let digest = Sha256::digest(cleaned.as_bytes());
        format!(r"\\.\pipe\tpt-sync-{}", hex::encode(&digest[..6]))
    } else {
        config_dir
            .join("daemon.sock")
            .to_string_lossy()
            .into_owned()
    }
}

/// Sends one request. Returns Err("daemon is not running") when no daemon
/// is listening on the endpoint.
pub fn call(config_dir: &Path, op: &str, job: Option<&str>) -> Result<Response, String> {
    let endpoint = endpoint(config_dir);
    let mut line = serde_json::to_string(&Request { op, job }).map_err(|e| e.to_string())?;
    line.push('\n');
    let reply = exchange(&endpoint, line.as_bytes())?;
    serde_json::from_str(&reply).map_err(|e| format!("bad daemon reply: {e}"))
}

#[cfg(windows)]
fn exchange(endpoint: &str, request: &[u8]) -> Result<String, String> {
    use std::fs::OpenOptions;
    // A named pipe opens like a file. Missing pipe means the daemon is down.
    let pipe = OpenOptions::new()
        .read(true)
        .write(true)
        .open(endpoint)
        .map_err(|_| "daemon is not running".to_string())?;
    let mut writer = pipe.try_clone().map_err(|e| e.to_string())?;
    writer.write_all(request).map_err(|e| e.to_string())?;
    let mut reader = BufReader::new(pipe);
    let mut out = String::new();
    reader.read_line(&mut out).map_err(|e| e.to_string())?;
    Ok(out)
}

#[cfg(unix)]
fn exchange(endpoint: &str, request: &[u8]) -> Result<String, String> {
    use std::os::unix::net::UnixStream;
    let mut stream =
        UnixStream::connect(endpoint).map_err(|_| "daemon is not running".to_string())?;
    stream.write_all(request).map_err(|e| e.to_string())?;
    let mut reader = BufReader::new(stream);
    let mut out = String::new();
    reader.read_line(&mut out).map_err(|e| e.to_string())?;
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn unix_endpoint_is_socket_in_config_dir() {
        if cfg!(unix) {
            let ep = endpoint(Path::new("/home/u/.config/tpt-sync"));
            assert_eq!(ep, "/home/u/.config/tpt-sync/daemon.sock");
        }
    }

    #[test]
    fn windows_endpoint_is_stable_and_case_insensitive() {
        if cfg!(windows) {
            let a = endpoint(Path::new(r"C:\Users\Me\AppData\Roaming\tpt-sync"));
            let b = endpoint(Path::new(r"c:\users\me\appdata\roaming\tpt-sync"));
            assert_eq!(a, b);
            assert!(a.starts_with(r"\\.\pipe\tpt-sync-"));
        }
    }

    /// Cross-language contract: engine/ipc.DefaultEndpoint in the Go module
    /// produces this exact pipe name for this config directory.
    #[test]
    fn windows_endpoint_matches_go_implementation() {
        if cfg!(windows) {
            let ep = endpoint(Path::new(r"C:\Users\phill\AppData\Roaming\tpt-sync"));
            assert_eq!(ep, r"\\.\pipe\tpt-sync-ae4163407807");
        }
    }

    #[test]
    fn request_omits_empty_job() {
        let s = serde_json::to_string(&Request {
            op: "status",
            job: None,
        })
        .unwrap();
        assert_eq!(s, r#"{"op":"status"}"#);
    }

    #[test]
    fn missing_daemon_is_reported_as_not_running() {
        let dir = std::env::temp_dir().join("tpt-sync-gui-test-nonexistent");
        let err = call(&dir, "ping", None).unwrap_err();
        assert_eq!(err, "daemon is not running");
    }
}
