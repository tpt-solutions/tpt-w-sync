# tpt-sync

A reliable, versioned backup & sync utility — an open-source alternative to
FreeFileSync and GoodSync, with no hidden licensing tiers.

tpt-sync is built by [TPT Solutions](https://github.com/tpt-solutions).

## Status

Early development (pre-v1). The engine core, local and versioned backups,
scheduling, encryption, and the CLI work end to end. Cloud/network backends,
the GUI, and OS-keychain storage are not yet implemented. See
[todo.md](todo.md) for the full roadmap and per-phase status.

## Quick start (CLI)

```sh
tpt-sync job add docs --source ~/Documents --dest /mnt/backup/docs \
    --exclude "*.tmp" --max-versions 30 --schedule "0 2 * * *" --watch
tpt-sync run docs --dry-run     # show the plan
tpt-sync run docs               # create a snapshot now
tpt-sync versions list docs
tpt-sync versions restore docs --path reports --to ~/restore
tpt-sync status
tpt-sync daemon                 # run scheduled and watched jobs in the foreground
```

While `tpt-sync daemon` is running, `run` and `status` talk to it over a
per-user control channel (a named pipe on Windows, a user-only socket
elsewhere), so manual and scheduled runs never overlap. Jobs edited while the
daemon runs take effect after it restarts.

Limit upload bandwidth per job with `--bandwidth-limit 5M` (bytes per second,
K/M/G suffixes).

A systemd user unit for the daemon is in `packaging/systemd/`.

Remote destinations:

```sh
# WebDAV (Nextcloud, ownCloud, NAS WebDAV shares, …)
tpt-sync job add cloud --source ~/Documents --url https://dav.example.com/remote.php/webdav \
    --dest backups/pc1 --username phil
tpt-sync secret set cloud password            # stored in the OS keychain

# S3-compatible storage (AWS, MinIO, B2 S3 API, …)
tpt-sync job add bucket --source ~/Documents --bucket my-backups --region eu-west-1 \
    --dest pc1 --username AKIAEXAMPLE
tpt-sync secret set bucket password           # the secret access key
```

Remote destinations keep full copies of each snapshot (no hardlinks), so they
use more storage than local ones. Remote endpoints must be https; credentials
are never sent over plain http.

Encrypted destinations: add `--encrypt`, then store the passphrase with
`tpt-sync secret set <job> passphrase` (it is kept in the OS keychain). Keep a
copy of the passphrase somewhere safe: without it, encrypted backups cannot be
recovered. `TPT_SYNC_PASSPHRASE` overrides the stored value for scripts.

Each destination directory holds its own `.tpt-sync/` metadata. Use a
dedicated destination per job; a plaintext job will refuse to write to an
encrypted destination, and vice versa.

## Design at a glance

- **Engine**: Go (`engine/`) — concurrent file I/O, network transfer,
  scheduling, destination backends, CLI.
- **Checksums**: Rust BLAKE3 helper (`hash/`, binary `tpt-sync-hash`), invoked
  by the Go engine as a subprocess.
- **GUI**: Tauri (Rust + TypeScript/Vite) tray/status app (`src-tauri/`, `src/`).
- **Sync model**: one-way mirror plus versioned backup. The destination is never
  a source of truth; changed or deleted source files are kept as versions.
- **Versioning**: timestamped snapshot folders with hardlink dedup
  (`--link-dest` style) where the destination supports hardlinks.
- **Targets**: Windows and Linux for v1.

Full specification: [spec.txt](spec.txt).

## Repository layout

| Path         | Contents                                              |
|--------------|-------------------------------------------------------|
| `engine/`    | Go sync core, scheduler, backends, CLI                |
| `hash/`      | Rust BLAKE3 checksum helper crate (`tpt-sync-hash`)   |
| `src-tauri/` | Tauri backend (planned, Phase 9)                      |
| `src/`       | TypeScript/Vite frontend (planned, Phase 9)           |

## Building

Requirements:

- Go 1.26+ (version pinned in `go.mod`)
- Rust 1.85.0 (pinned in `rust-toolchain.toml`; `rustup` installs it automatically)

```sh
# Rust hash helper (the Go engine calls this binary for checksums)
cargo build --workspace

# Go engine. Point the tests at the helper to run the integration tests.
TPT_SYNC_HASH="$PWD/target/debug/tpt-sync-hash" go test ./...
go build -o tpt-sync ./engine/cmd/tpt-sync

cargo test --workspace
```

The engine finds `tpt-sync-hash` via `$TPT_SYNC_HASH`, then next to the
`tpt-sync` executable, then on `PATH`.

Design notes: [docs/archon-design.md](docs/archon-design.md) (Archon, future work).

## License

Dual-licensed under either of

- [MIT License](LICENSE-MIT)
- [Apache License, Version 2.0](LICENSE-APACHE)

at your option.

### Contributing

Unless you explicitly state otherwise, any contribution intentionally submitted
for inclusion in this work is licensed as above, without any additional terms
or conditions.

Source files carry an SPDX header identifying the license:

```text
// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions
```

(Use the `#` comment form in TOML, YAML and shell files.)
