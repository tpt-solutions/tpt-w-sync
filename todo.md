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

Decisions made while implementing (recommendations adopted):
- **Snapshot model**: each run writes a complete tree under
  `.tpt-sync/versions/<ts>/`; the newest complete snapshot is named in
  `.tpt-sync/latest`. There is no separate mirror copy. "Version-and-replace"
  in the plan is therefore realised as "write a new snapshot".
- **Write safety**: files are written to a temp file and renamed into place,
  never modified in place, so hardlinked older versions stay intact.
- **Encryption dedup**: random nonces mean identical plaintext produces
  different ciphertext, so dedup is by name, not content. Unchanged files are
  linked from the previous snapshot using keyed obfuscated names; an encrypted
  manifest per snapshot maps names back to paths.
- **Job config format**: JSON (not TOML), to avoid an extra dependency.
- **Module Go version**: 1.26 (required by the current `golang.org/x/crypto`).

---

## Status Summary

| Phase | Area | Progress |
|-------|------|----------|
| 0  | Project Setup & Licensing | 🟡 In Progress (local items done; CI untested on GitHub; tauri config, org hosting pending) |
| 1  | Core Sync Engine & Job Model (Go) | 🟢 Done except OS-keychain credentials |
| 2  | Rust BLAKE3 Checksum Helper | 🟢 Done except benchmarks and installer bundling |
| 3  | Destination Backends: Local, SMB, WebDAV, S3 | 🟡 Local, WebDAV, S3 done (tested against in-process servers); SMB not started |
| 4  | Cloud Destination Backends: Google Drive, Dropbox, Azure Blob | ❌ Not Started (needs provider app registrations) |
| 5  | Versioned Snapshots & Hardlink Dedup | 🟢 Done except size-based retention |
| 6  | Scheduling: Cron Jobs & Real-Time Watch | 🟢 Done except configurable missed-run policy and OS service install |
| 7  | Optional End-to-End Encryption | 🟢 Core and OS-keychain storage done; recovery-phrase (BIP-39) not implemented, warning UX only |
| 8  | CLI | 🟡 Core commands done (incl. `secret`, remote destinations); IPC and CLI tests pending |
| 9  | Tauri Tray/GUI Status App | ❌ Not Started |
| 10 | Linux Filesystem-Specific Support | ❌ Not Started (needs ext4/btrfs/zfs test targets) |
| 11 | Archon Support (Design Only) | 🟡 Design doc written; stub backend pending |
| 12 | Performance & Concurrency Polish | ❌ Not Started |
| 13 | Testing & QA | 🟡 Unit and end-to-end smoke tests done; integration and manual QA pending |
| 14 | Release v1 | ❌ Not Started |

Verification status: Rust helper tests pass (`cargo test`). Go tests pass on
Windows (`go test ./...`, 38 engine/backend/keychain tests, including the real
helper integration tests, the encryption tests, and WebDAV/S3 full-cycle tests
against in-process servers). The race detector and Linux runs are configured in
CI but have not been run locally (race needs cgo). The WebDAV and S3 backends
have not been run against a real NAS, Nextcloud, AWS, or MinIO server.

---

## Phase 0 — Project Setup & Licensing
- [x] Init git repo, `.gitignore` (Go + Rust + Node/Tauri targets + Windows build artifacts)
- [x] Add `LICENSE-MIT` and `LICENSE-APACHE` (dual license, TPT Solutions copyright)
- [x] Add SPDX dual-license header/notice convention for source files (documented in README)
- [ ] Repo layout: `engine/` (Go sync core, scheduler, backends, CLI), `hash/`
      (Rust BLAKE3 helper binary crate), `src-tauri/` (Tauri backend), `src/`
      (TS/Vite frontend, no framework), shared module/workspace metadata
      (`license`, `authors = ["TPT Solutions"]`, `repository`)
      — partial: `engine/`, `hash/`, and workspace metadata exist; `src-tauri/` and `src/` pending Phase 9
- [x] `go.mod` module setup, `rust-toolchain.toml` (MSRV pin), `rustfmt.toml`, `clippy.toml`
- [ ] Configure `tauri.conf.json` (app id, window defaults, bundle identifiers for tpt-sync)
      — pending: needs Tauri scaffold (`src-tauri/`), deferred to Phase 9
- [x] `README.md` (from spec.txt), build instructions, license section
      — badges not yet added
- [x] Set up CI (GitHub Actions, build+lint+test on Windows + Linux runners)
      — `.github/workflows/ci.yml`; Rust steps verified locally; Go steps not yet run on GitHub
- [ ] Repo hosting under `tpt-solutions` GitHub org — not done (outward-facing; needs your action)

## Phase 1 — Core Sync Engine & Job Model (Go)
- [x] Sync job model: source path, destination (backend + path), direction
      (one-way mirror), include/exclude filters, versioning policy
      — `engine/job.go`, `engine/filter.go`
- [x] Filesystem walker/diffing: compare source vs. last-known destination
      state (size, mtime, then checksum via Phase 2 helper on mismatch)
      — `engine/plan.go`
- [x] Concurrency model: worker pool for parallel file transfers/hashing
      across a job, bounded by configurable concurrency limit
      — `engine/run.go` (`executor`); hashing is batched in the helper
- [x] Transfer plan model: create/update/delete actions, dry-run mode
      (delete is realised by omitting the file from the new snapshot)
- [x] Job execution engine: apply a transfer plan, progress reporting,
      cancellation. Resumability: each file is written atomically and a
      failed or cancelled run discards its partial snapshot, so the next run
      re-plans from the last good snapshot (no partial-snapshot resume).
- [x] Config persistence (job definitions in JSON; destination passwords and
      passphrases in the OS keychain)
- [x] Unit tests: diffing logic, transfer plan generation, dry-run correctness
      — `engine/run_test.go`, `engine/filter_test.go`

## Phase 2 — Rust BLAKE3 Checksum Helper
- [x] `tpt-sync-hash` Rust binary: BLAKE3 checksum over one or many files,
      streaming (1 MiB read buffer; memory does not scale with file size)
      — `hash/src/main.rs`
- [x] CLI interface: file path args or stdin, structured (JSON lines) output
      for easy parsing by the Go engine
- [x] Batch mode for hashing many files in one invocation (512 paths per
      process, `--stdin`)
- [x] Go wrapper: subprocess invocation, streaming stdout parsing, error
      propagation, binary discovery (`$TPT_SYNC_HASH`, next to executable, PATH)
      — `engine/hasher.go`. Bundling alongside the installer is in Phase 14.
- [ ] Benchmarks: throughput vs. a pure-Go BLAKE3 baseline to validate the
      subprocess-boundary approach doesn't regress performance on large trees
- [x] Unit tests: hash correctness against known BLAKE3 test vectors
      (empty input and "abc"), streaming vs one-shot, JSON escaping
      — plus Go integration tests against the real binary (`engine/hasher_test.go`)

## Phase 3 — Destination Backends: Local, SMB, WebDAV, S3
- [x] Shared `Backend` interface (walk/stat/open/write/link/delete/list dirs/
      remove tree, capability flags e.g. hardlink support)
      — `engine/backend/backend.go`. Rename is not in the interface: `Write`
      is already atomic (temp file + rename).
- [x] Local filesystem backend (Windows + Linux paths, hardlink support probe,
      path-escape rejection, symlinks skipped) — `engine/backend/local.go`
- [ ] SMB backend (native SMB2/3 client, credential handling, share mounting)
- [x] WebDAV backend (HTTP basic auth; refuses credentials over plain http
      unless a test opts in). Digest/bearer auth not implemented.
      — `engine/backend/webdav.go`, tested against `engine/internal/davfake`
- [x] S3-compatible backend (AWS S3, MinIO, any S3 API via minio-go; multipart
      uploads handled by the client; credentials refused over plain http)
      — `engine/backend/s3.go`, tested over TLS against gofakes3. Not tested
      against real AWS, MinIO, or B2.
- [x] Backend capability negotiation: `Capabilities().Hardlink` selects
      hardlink dedup or full-copy fallback (mechanism in place; exercised
      only by local backend so far)
- [ ] Integration tests per backend (local always; SMB/WebDAV/S3 against
      test doubles or containerized test servers in CI) — local covered

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
- [x] Timestamped version directory scheme (`.tpt-sync/versions/<ts>/`)
      per destination, with `.tpt-sync/latest` marking the newest complete one
- [x] Hardlink-based dedup: unchanged files link to the previous version
      instead of being re-copied (verified with `os.SameFile`)
- [x] Fallback full-copy versioning for backends without hardlink support
      (driven by `Capabilities().Hardlink`)
- [~] Retention policy: max versions ✓, max age ✓; max total size pending
      (needs a unique-inode size accounting pass)
- [x] Restore flow: restore a file or whole tree from a given snapshot (or the
      latest) to an arbitrary path — CLI `versions restore`. Restoring back
      into the source in place is not implemented; restore to a chosen path
      is the supported flow.
- [x] Unit tests: hardlink dedup correctness, retention pruning logic,
      restore correctness (hardlink mode covered; full-copy mode not yet tested)

## Phase 6 — Scheduling: Cron Jobs & Real-Time Watch
- [x] Cron-style schedule parser/runner for periodic jobs (per-job schedule)
      — `engine/schedule/cron.go` (5-field, lists/ranges/steps, @aliases,
      DOM/DOW OR semantics)
- [x] Real-time filesystem watch: `fsnotify` (inotify on Linux,
      ReadDirectoryChangesW on Windows), new subdirectories added automatically
      — `engine/schedule/watch.go`
- [x] Debounce/coalesce rapid successive changes before triggering a sync run
      (quiet period plus max-wait) — `engine/schedule/debounce.go`
- [x] Scheduler daemon hosting cron and watch triggers, serialising runs per
      job and queueing one follow-up run — `engine/daemon/daemon.go`,
      `tpt-sync daemon`. Running as an OS service (systemd unit / Windows
      service) is Phase 10/14.
- [~] Missed-run handling: a run missed while stopped is caught up once at
      startup. Configurable policy (run / skip / on next wake) pending.
- [x] Unit tests: cron scheduling correctness, debounce timing and max-wait,
      watch event delivery. Missed-run logic is not yet unit-tested.

## Phase 7 — Optional End-to-End Encryption
- [x] Per-job opt-in E2E encryption toggle (`--encrypt`), passphrase supplied
      via `TPT_SYNC_PASSPHRASE` (interim)
- [x] Argon2id key derivation → AES-256-GCM, streamed in 64 KiB authenticated
      chunks (detects reordering, truncation, trailing data) — `engine/crypt/`
      (mirrors tpt-clipboard's pattern)
- [x] Encrypted filename/path handling: keyed (HMAC) obfuscated file names and
      a sealed per-snapshot manifest, so the destination reveals no paths
- [x] Key/passphrase storage: OS keychain (Windows Credential Manager, macOS
      Keychain, Linux Secret Service via go-keyring), with a non-echoing prompt
      and a confirmation step and a loss warning. `TPT_SYNC_PASSPHRASE` overrides.
      — `engine/secrets/`. Not yet done: a BIP-39 recovery phrase (warning only).
- [x] Tests: encryption round-trip at chunk boundaries, tamper/truncation
      detection, wrong-passphrase rejection, no plaintext on disk, dedup of
      unchanged files across encrypted snapshots
      — Note: a touch-only mtime change re-encrypts the file (plaintext
      checksum is not stored), a known cost of the dedup-by-name design

## Phase 8 — CLI
- [x] `tpt-sync job add/edit/list/show/remove` (job definitions)
- [x] `tpt-sync run <job>` (on-demand run, `--dry-run` support)
- [x] `tpt-sync schedule enable/disable <job>` (cron + watch control)
- [x] `tpt-sync versions list/restore <job> [--version <ts>] [--path REL]`
- [~] `tpt-sync status` (last-run results and next scheduled run). Live
      "currently running" state needs the daemon IPC below.
- [ ] CLI talks to the same engine/daemon used by the GUI (local
      socket/named pipe IPC). Today `run` executes in-process and the daemon is
      a separate foreground process; they coordinate only through the store.
- [ ] CLI unit/integration tests (command parsing and output not yet covered;
      engine behaviour is covered)

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
- [x] `docs/archon-design.md`: design for zero-copy, space-efficient local
      versioning via B-Link tree deltas through `tpt-archon-core`, enabling
      instantaneous local snapshots before syncing to external targets
- [x] Open Questions (pending tpt-archon team): recorded in
      `docs/archon-design.md` (delta API shape, mapping to versioning model,
      file addressing, hardlink fallback interaction, consistency, encryption)
- [ ] Post-v1 `archon` backend/feature flag plan (stub backend,
      `is_available() -> false`, `Unsupported` errors until implemented) —
      plan is in the design doc; stub not yet written

## Phase 12 — Performance & Concurrency Polish
- [ ] Large-tree performance tuning (millions of small files scenario):
      walker/diffing throughput, worker pool tuning
- [ ] Bandwidth throttling (global + per-job limits) for network destinations
- [ ] Memory profiling (engine daemon, checksum helper batching, WebView)
- [ ] Resumable transfer correctness under interruption (network drop,
      process kill) across all backends
- [ ] App icon, branding assets (TPT Solutions)

## Phase 13 — Testing & QA
- [~] Unit tests: diffing, versioning/dedup, scheduling, encryption (per-phase
      tests in place; consolidated coverage pass pending)
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
