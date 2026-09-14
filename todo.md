# tpt-sync — Project Checklist

TPT Solutions — dual-licensed under MIT OR Apache-2.0.

A reliable, versioned backup & sync utility (FreeFileSync/GoodSync alternative).
Go core (concurrent file I/O, network transfer, scheduling, destination backends)
+ Rust BLAKE3 checksumming helper. Tauri (Rust + TS/Vite) tray/GUI status app,
matching the pattern used by sibling tpt- apps. Targets Windows and Linux for v1
(native ext4/btrfs/zfs support). Archon (zero-copy B-Link tree delta snapshots
via tpt-archon-core) is design-only until the core sync engine is solid.

Reference: Archon platform — https://github.com/tpt-solutions/tpt-archon

Decisions locked in for this roadmap:
- **Sync direction**: one-way mirror + versioned backup only (source →
  destination). No two-way sync / conflict resolution in v1 — the destination
  is never treated as a source of truth, so deleted/changed source files are
  preserved as versions rather than lost or reconciled.
- **Versioning model**: timestamped version folders with hardlink dedup
  (rsync `--link-dest` style) — each snapshot looks like a full logical tree,
  but unchanged files are hardlinked to the previous version to save space.
  Chosen over a content-addressed (restic/borg-style) store for v1 simplicity
  and because it works on any destination that supports hardlinks.
- **Destinations in v1**: local drives, SMB, WebDAV, S3-compatible (as
  spec'd), **plus** broader cloud: Google Drive, Dropbox, Azure Blob (each via
  its own OAuth/API integration — significant scope, called out as its own
  phase).
- **Go/Rust integration**: Rust BLAKE3 checksumming ships as a **standalone
  CLI helper binary** (`tpt-sync-hash`), invoked by the Go engine as a
  subprocess (stdin/stdout or file-arg interface) — not cgo/FFI. Chosen
  because it keeps the Go and Rust build/cross-compilation pipelines
  decoupled, which matters given Windows + Linux + (future) Archon targets.
- **UI**: CLI + tray/GUI status app, matching sibling apps. GUI stack is
  **Tauri** (Rust + TS/Vite), consistent with tpt-fonts.
- **Encryption**: optional end-to-end encryption for cloud destinations,
  following tpt-clipboard's pattern (Argon2id → AES-256-GCM), off by default,
  enabled per-job with a user passphrase.
- **Scheduling**: both cron-style periodic jobs (e.g. nightly at 2am) *and*
  real-time filesystem watch (fsnotify / `ReadDirectoryChangesW`) for
  near-instant sync of changed files.
- Company: TPT Solutions. License: dual **MIT OR Apache-2.0**.

---

## Status Summary

| Phase | Area | Progress |
|-------|------|----------|
| 0  | Project Setup & Licensing | ❌ Not Started |
| 1  | Core Sync Engine & Job Model (Go) | ❌ Not Started |
| 2  | Rust BLAKE3 Checksum Helper | ❌ Not Started |
| 3  | Destination Backends: Local, SMB, WebDAV, S3 | ❌ Not Started |
| 4  | Cloud Destination Backends: Google Drive, Dropbox, Azure Blob | ❌ Not Started |
| 5  | Versioned Snapshots & Hardlink Dedup | ❌ Not Started |
| 6  | Scheduling: Cron Jobs & Real-Time Watch | ❌ Not Started |
| 7  | Optional End-to-End Encryption | ❌ Not Started |
| 8  | CLI | ❌ Not Started |
| 9  | Tauri Tray/GUI Status App | ❌ Not Started |
| 10 | Linux Filesystem-Specific Support | ❌ Not Started |
| 11 | Archon Support (Design Only) | ❌ Not Started |
| 12 | Performance & Concurrency Polish | ❌ Not Started |
| 13 | Testing & QA | ❌ Not Started |
| 14 | Release v1 | ❌ Not Started |

---

## Phase 0 — Project Setup & Licensing
- [ ] Init git repo, `.gitignore` (Go + Rust + Node/Tauri targets + Windows build artifacts)
- [ ] Add `LICENSE-MIT` and `LICENSE-APACHE` (dual license, TPT Solutions copyright)
- [ ] Add SPDX dual-license header/notice convention for source files
- [ ] Repo layout: `engine/` (Go sync core, scheduler, backends, CLI), `hash/`
      (Rust BLAKE3 helper binary crate), `src-tauri/` (Tauri backend), `src/`
      (TS/Vite frontend, no framework), shared module/workspace metadata
      (`license`, `authors = ["TPT Solutions"]`, `repository`)
- [ ] `go.mod` module setup, `rust-toolchain.toml` (MSRV pin), `rustfmt.toml`, `clippy.toml`
- [ ] Configure `tauri.conf.json` (app id, window defaults, bundle identifiers for tpt-sync)
- [ ] `README.md` (from spec.txt), build instructions, license badges
- [ ] Set up CI (GitHub Actions, build+lint+test on Windows + Linux runners)
- [ ] Repo hosting under `tpt-solutions` GitHub org

## Phase 1 — Core Sync Engine & Job Model (Go)
- [ ] Sync job model: source path, destination (backend + path), direction
      (one-way mirror), include/exclude filters, versioning policy
- [ ] Filesystem walker/diffing: compare source vs. last-known destination
      state (size, mtime, then checksum via Phase 2 helper on mismatch)
- [ ] Concurrency model: worker pool for parallel file transfers/hashing
      across a job, bounded by configurable concurrency limit
- [ ] Transfer plan model: create/update/delete/version-and-replace actions,
      dry-run mode
- [ ] Job execution engine: apply a transfer plan, progress reporting,
      cancellation, resumability after interruption
- [ ] Config persistence (job definitions, destination credentials via OS
      keychain) — JSON or TOML
- [ ] Unit tests: diffing logic, transfer plan generation, dry-run correctness

## Phase 2 — Rust BLAKE3 Checksum Helper
- [ ] `tpt-sync-hash` Rust binary: BLAKE3 checksum over one or many files,
      streaming (no full-file memory load for large files)
- [ ] CLI interface: file path args or stdin, structured (JSON lines) output
      for easy parsing by the Go engine
- [ ] Batch mode for hashing many files in one invocation (amortize process
      spawn cost across a sync job)
- [ ] Go wrapper: subprocess invocation, streaming stdout parsing, error
      propagation, binary discovery/bundling alongside the Go engine
- [ ] Benchmarks: throughput vs. a pure-Go BLAKE3 baseline to validate the
      subprocess-boundary approach doesn't regress performance on large trees
- [ ] Unit tests: hash correctness against known BLAKE3 test vectors

## Phase 3 — Destination Backends: Local, SMB, WebDAV, S3
- [ ] Shared `Backend` interface (list/stat/read/write/delete/rename,
      capability flags e.g. hardlink support)
- [ ] Local filesystem backend (Windows + Linux paths, hardlink support probe)
- [ ] SMB backend (native SMB2/3 client, credential handling, share mounting)
- [ ] WebDAV backend (HTTP client, basic/digest/bearer auth)
- [ ] S3-compatible backend (AWS S3 + MinIO/B2-via-S3-API, multipart upload
      for large files, credential handling)
- [ ] Backend capability negotiation: how versioning (Phase 5) degrades on
      backends without native hardlink support (e.g. S3, WebDAV)
- [ ] Integration tests per backend (local always; SMB/WebDAV/S3 against
      test doubles or containerized test servers in CI)

## Phase 4 — Cloud Destination Backends: Google Drive, Dropbox, Azure Blob
- [ ] OAuth2 flow handling per provider (Google Drive, Dropbox, Azure Blob),
      token storage in OS keychain, refresh handling
- [ ] Google Drive backend (Drive API v3: folder mapping, chunked upload)
- [ ] Dropbox backend (Dropbox API v2: chunked upload, path conventions)
- [ ] Azure Blob backend (Blob Storage REST API, container mapping, block
      upload for large files)
- [ ] Rate-limit/backoff handling per provider's API quotas
- [ ] Capability flags: these providers have no hardlink support, so versioning
      falls back to full-copy-per-version (see Phase 5) — document the
      resulting storage cost tradeoff in the destination picker UI
- [ ] Integration tests against provider sandboxes/mocked APIs

## Phase 5 — Versioned Snapshots & Hardlink Dedup
- [ ] Timestamped version directory scheme (e.g. `.tpt-sync/versions/<ts>/`)
      per destination
- [ ] Hardlink-based dedup: unchanged files link to the previous version
      instead of being re-copied, on backends that support it (local, SMB
      with appropriate filesystem)
- [ ] Fallback full-copy versioning for backends without hardlink support
      (S3, WebDAV, cloud providers — Phase 3/4 capability flag driven)
- [ ] Retention policy: max versions / max age / max total size, pruning job
- [ ] Restore flow: browse versions, restore a file or whole tree from a
      given snapshot back to source or an arbitrary path
- [ ] Unit tests: hardlink dedup correctness, retention pruning logic,
      restore correctness across both hardlink and full-copy modes

## Phase 6 — Scheduling: Cron Jobs & Real-Time Watch
- [ ] Cron-style schedule parser/runner for periodic jobs (per-job schedule)
- [ ] Real-time filesystem watch: `fsnotify` (Linux) / `ReadDirectoryChangesW`
      (Windows) triggering near-instant sync of changed paths
- [ ] Debounce/coalesce rapid successive changes before triggering a sync run
- [ ] Scheduler daemon/service process hosting both cron and watch triggers,
      running independent of the GUI
- [ ] Missed-run handling (e.g. machine was off at scheduled time — run on
      next wake, configurable)
- [ ] Unit tests: cron scheduling correctness, debounce timing, missed-run logic

## Phase 7 — Optional End-to-End Encryption
- [ ] Per-job opt-in E2E encryption toggle with user passphrase
- [ ] Argon2id key derivation → AES-256-GCM envelope encryption per file
      (mirrors tpt-clipboard's pattern)
- [ ] Encrypted filename/path handling (avoid leaking directory structure to
      the destination when encryption is enabled, where feasible per backend)
- [ ] Key/passphrase storage: OS keychain, with recovery-phrase warning UX
      (passphrase loss = unrecoverable data)
- [ ] Tests: encryption round-trip, wrong-passphrase rejection, interaction
      with versioning (Phase 5) and hardlink dedup (ciphertext must still
      dedup correctly when content is unchanged)

## Phase 8 — CLI
- [ ] `tpt-sync job add/list/edit/remove` (job definitions)
- [ ] `tpt-sync run <job>` (on-demand run, `--dry-run` support)
- [ ] `tpt-sync schedule enable/disable <job>` (cron + watch control)
- [ ] `tpt-sync versions list/restore <job> [--version <ts>]`
- [ ] `tpt-sync status` (running jobs, last-run results, next scheduled runs)
- [ ] CLI talks to the same engine/daemon used by the GUI (local
      socket/named pipe IPC)
- [ ] CLI unit/integration tests

## Phase 9 — Tauri Tray/GUI Status App
- [ ] Tray icon + menu: job status at a glance, quick run/pause, open main window
- [ ] Main window: job list, per-job status/progress, last-run history
- [ ] Job editor: source/destination pickers (incl. cloud OAuth connect flow),
      schedule config, versioning/retention settings, encryption toggle
- [ ] Version browser/restore UI (Phase 5 restore flow)
- [ ] Settings: autostart toggle, global concurrency/bandwidth limits,
      light/dark theme
- [ ] IPC between the Go scheduler/engine daemon and the Tauri frontend
      (local socket/named pipe), consistent with the CLI's IPC (Phase 8)

## Phase 10 — Linux Filesystem-Specific Support
- [ ] Native ext4/btrfs/zfs support: verify hardlink/versioning behavior per
      filesystem, detect filesystem type to choose the right versioning strategy
- [ ] btrfs/zfs snapshot integration as an *optional* faster-path alternative
      to hardlink dedup when the destination filesystem supports native
      snapshots (still exposed through the same versioning UI/CLI)
- [ ] Linux packaging groundwork (paths, permissions, systemd user unit for
      the scheduler daemon)
- [ ] Verify behavior on at least one ext4 and one btrfs/zfs test target

## Phase 11 — Archon Support (Design Only)
- [ ] `docs/archon-design.md`: design for zero-copy, space-efficient local
      versioning via B-Link tree deltas through `tpt-archon-core`, enabling
      instantaneous local snapshots before syncing to external targets
- [ ] Open Questions (pending tpt-archon team): B-Link tree delta API shape,
      how Archon-native snapshots map onto the versioning model from Phase 5,
      interaction with hardlink-dedup fallback for non-Archon destinations
- [ ] Post-v1 `archon` backend/feature flag plan (stub backend,
      `is_available() -> false`, `Unsupported` errors until implemented)

## Phase 12 — Performance & Concurrency Polish
- [ ] Large-tree performance tuning (millions of small files scenario):
      walker/diffing throughput, worker pool tuning
- [ ] Bandwidth throttling (global + per-job limits) for network destinations
- [ ] Memory profiling (engine daemon, checksum helper batching, WebView)
- [ ] Resumable transfer correctness under interruption (network drop,
      process kill) across all backends
- [ ] App icon, branding assets (TPT Solutions)

## Phase 13 — Testing & QA
- [ ] Unit tests: diffing, versioning/dedup, scheduling, encryption (already
      tracked per-phase above) — consolidated coverage pass
- [ ] Integration tests: end-to-end job run across each backend (local, SMB,
      WebDAV, S3, Google Drive, Dropbox, Azure Blob)
- [ ] Manual QA: Windows 10/11
- [ ] Manual QA: Linux (ext4 + btrfs/zfs, at least one major distro)
- [ ] Stress test: large tree (100k+ files) — sync throughput, checksum
      helper overhead, memory usage
- [ ] Security review: credential storage (keychain), encryption
      implementation, path traversal in job configs, IPC boundary (tray↔engine↔CLI)

## Phase 14 — Release v1
- [ ] Finalize versioning scheme (SemVer) and changelog process
- [ ] Windows installer (MSI/NSIS via Tauri bundler), autostart registration,
      code-signing decision
- [ ] Linux packaging (.deb/.rpm/AppImage via Tauri bundler), systemd user
      unit for the scheduler daemon
- [ ] Publish LICENSE files + notices in release artifacts
- [ ] User-facing docs (install guide, CLI reference, job/versioning/encryption explainer)
- [ ] Tag v1.0.0 release, publish to GitHub (tpt-solutions org)
- [ ] Post-release triage process for bug reports

---

## Backlog / Future Phases (post-v1)

- [ ] Two-way sync with conflict resolution (FreeFileSync-style)
- [ ] btrfs/zfs native snapshot mode promoted from optional to default where available
- [ ] Additional cloud providers (Backblaze B2 native API, OneDrive)
- [ ] Bandwidth scheduling (e.g. throttle during business hours)
- [ ] Archon backend implementation (post design-only phase, pending public tpt-archon API)
- [ ] macOS support
- [ ] Web-based remote monitoring dashboard for multi-machine job status
