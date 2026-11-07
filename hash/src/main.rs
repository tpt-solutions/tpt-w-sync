// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

//! `tpt-sync-hash` — streaming BLAKE3 checksum helper.
//!
//! Usage:
//!   tpt-sync-hash [PATH...]      hash the given files
//!   tpt-sync-hash --stdin        read newline-separated paths from stdin
//!   tpt-sync-hash -- [PATH...]   treat following args as paths even if they start with `-`
//!
//! Output: one JSON object per input path, on stdout, in input order:
//!   {"path":"<path>","hash":"<64 hex chars>"}            on success
//!   {"path":"<path>","error":"<message>"}                 on failure
//!
//! Batch mode: all paths are processed in a single process invocation so the
//! Go engine pays process-spawn cost once per batch rather than once per file.
//! Files are streamed in fixed-size chunks; memory use does not scale with
//! file size. Exit status is 0 if every file hashed, 1 if any failed, 2 on
//! usage error.

use std::fs::File;
use std::io::{self, BufRead, BufReader, BufWriter, Write};
use std::path::Path;
use std::process::ExitCode;

/// Hash a file by streaming it through BLAKE3 and return the 64-char hex digest.
pub fn hash_file(path: &Path) -> io::Result<String> {
    let file = File::open(path)?;
    let mut reader = BufReader::with_capacity(1 << 20, file);
    let mut hasher = blake3::Hasher::new();
    hasher.update_reader(&mut reader)?;
    Ok(hasher.finalize().to_hex().to_string())
}

/// Escape a string as a JSON string literal (including the surrounding quotes).
pub fn json_string(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

/// Format one JSON-lines record for a path and its hashing result.
pub fn format_record(path: &str, result: &io::Result<String>) -> String {
    match result {
        Ok(hash) => format!(
            "{{\"path\":{},\"hash\":{}}}",
            json_string(path),
            json_string(hash)
        ),
        Err(e) => format!(
            "{{\"path\":{},\"error\":{}}}",
            json_string(path),
            json_string(&e.to_string())
        ),
    }
}

fn run(paths: Vec<String>, out: &mut impl Write) -> io::Result<bool> {
    let mut all_ok = true;
    for p in &paths {
        let result = hash_file(Path::new(p));
        if result.is_err() {
            all_ok = false;
        }
        writeln!(out, "{}", format_record(p, &result))?;
    }
    out.flush()?;
    Ok(all_ok)
}

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();

    let paths: Vec<String> = if args.first().map(String::as_str) == Some("--stdin") {
        if args.len() > 1 {
            eprintln!("tpt-sync-hash: --stdin takes no other arguments");
            return ExitCode::from(2);
        }
        let stdin = io::stdin();
        let mut v = Vec::new();
        for line in stdin.lock().lines() {
            match line {
                Ok(l) if !l.is_empty() => v.push(l),
                Ok(_) => {}
                Err(e) => {
                    eprintln!("tpt-sync-hash: reading stdin: {e}");
                    return ExitCode::from(2);
                }
            }
        }
        v
    } else if args.first().map(String::as_str) == Some("--") {
        args[1..].to_vec()
    } else if args.iter().any(|a| a.starts_with('-') && a != "-") {
        eprintln!("usage: tpt-sync-hash [--stdin | -- PATH...| PATH...]");
        return ExitCode::from(2);
    } else {
        args
    };

    if paths.is_empty() {
        eprintln!("usage: tpt-sync-hash [--stdin | -- PATH...| PATH...]");
        return ExitCode::from(2);
    }

    let stdout = io::stdout();
    let mut out = BufWriter::new(stdout.lock());
    match run(paths, &mut out) {
        Ok(true) => ExitCode::SUCCESS,
        Ok(false) => ExitCode::from(1),
        Err(e) => {
            eprintln!("tpt-sync-hash: writing output: {e}");
            ExitCode::from(1)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Official BLAKE3 test vectors (empty input).
    const EMPTY_HASH: &str = "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262";
    // blake3("abc")
    const ABC_HASH: &str = "6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85";

    fn temp_file(name: &str, contents: &[u8]) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("tpt-sync-hash-test-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join(name);
        let mut f = File::create(&p).unwrap();
        f.write_all(contents).unwrap();
        p
    }

    #[test]
    fn hashes_empty_file_to_known_vector() {
        let p = temp_file("empty.bin", b"");
        assert_eq!(hash_file(&p).unwrap(), EMPTY_HASH);
    }

    #[test]
    fn hashes_known_content() {
        let p = temp_file("abc.bin", b"abc");
        assert_eq!(hash_file(&p).unwrap(), ABC_HASH);
    }

    #[test]
    fn streaming_matches_one_shot_for_large_input() {
        // Larger than the 1 MiB read buffer, so multiple read chunks are exercised.
        let data: Vec<u8> = (0..3_000_000u32).map(|i| (i % 251) as u8).collect();
        let p = temp_file("large.bin", &data);
        assert_eq!(
            hash_file(&p).unwrap(),
            blake3::hash(&data).to_hex().to_string()
        );
    }

    #[test]
    fn missing_file_is_an_error() {
        assert!(hash_file(Path::new("definitely/does/not/exist.bin")).is_err());
    }

    #[test]
    fn json_escapes_special_characters() {
        assert_eq!(json_string("a\"b\\c\nd"), "\"a\\\"b\\\\c\\nd\"");
        assert_eq!(json_string("\u{1}"), "\"\\u0001\"");
    }

    #[test]
    fn record_format_success_and_error() {
        let ok = format_record("x", &Ok("ff".to_string()));
        assert_eq!(ok, "{\"path\":\"x\",\"hash\":\"ff\"}");
        let err = format_record("y", &Err(io::Error::other("boom")));
        assert_eq!(err, "{\"path\":\"y\",\"error\":\"boom\"}");
    }

    #[test]
    fn run_reports_failure_but_continues() {
        let good = temp_file("good.bin", b"abc");
        let paths = vec![
            good.to_string_lossy().into_owned(),
            "no/such/file".to_string(),
        ];
        let mut buf = Vec::new();
        let ok = run(paths, &mut buf).unwrap();
        assert!(!ok);
        let lines: Vec<&str> = std::str::from_utf8(&buf).unwrap().lines().collect();
        assert_eq!(lines.len(), 2);
        assert!(lines[0].contains(ABC_HASH));
        assert!(lines[1].contains("\"error\""));
    }
}
