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
| 2  | Rust BLAKE3 Checksum Helper | 🟢 Done; benchmarked (see Phase 2 notes); installer bundling pending |
| 3  | Destination Backends: Local, SMB, WebDAV, S3 | 🟡 Local, WebDAV, S3 done (tested against in-process servers); SMB not started |
| 4  | Cloud Destination Backends: Google Drive, Dropbox, Azure Blob | ❌ Not Started (needs provider app registrations) |
| 5  | Versioned Snapshots & Hardlink Dedup | 🟢 Done except size-based retention |
| 6  | Scheduling: Cron Jobs & Real-Time Watch | 🟢 Done except configurable missed-run policy and OS service install |
| 7  | Optional End-to-End Encryption | 🟢 Core and OS-keychain storage done; recovery-phrase (BIP-39) not implemented, warning UX only |
| 8  | CLI | 🟢 Core commands, daemon IPC and CLI tests done; GUI-side IPC clients are Phase 9 |
| 9  | Tauri Tray/GUI Status App | 🟡 Tray, job list with live state, Run now, and close-to-tray done; job editor, version browser, and settings pending |
| 10 | Linux Filesystem-Specific Support | 🟡 systemd user unit written (untested on Linux); filesystem-specific work not started (needs ext4/btrfs/zfs targets) |
| 11 | Archon Support (Design Only) | 🟢 Design doc and rejecting stub done (`archon` store fails validation) |
| 12 | Performance & Concurrency Polish | 🟡 Per-job bandwidth limit and hashing benchmarks done; global limits, profiling, large-tree stress pending |
| 13 | Testing & QA | 🟡 Unit, CLI, daemon, IPC and fuzz tests done; manual QA and real-server integration pending |
| 14 | Release v1 | ❌ Not Started |

Verification status: Rust helper tests pass (`cargo test`). Go tests pass on
Windows (`go test ./...`, across the engine, backend, crypt, schedule, secrets,
ipc, daemon and CLI packages, including the real helper integration tests, the
encryption tests, the named-pipe control channel, and WebDAV/S3 full-cycle
tests against in-process servers). Fuzz targets were run briefly. The race detector and Linux runs are configured in
CI but have not been run locally (race needs cgo). The WebDAV and S3 backends
have not been run against a real NAS, Nextcloud, AWS, or MinIO server.

---

## Phase 0 — Project Setup & Licensing
- [x] Init git repo, `.gitignore` (Go + Rust + Node/Tauri targets + Windows build artifacts)
- [x] Add `LICENSE-MIT` and `LICENSE-APACHE` (dual license, TPT Solutions copyright)
- [x] Add SPDX dual-license header/notice convention for source files (documented in README)
- [x] Repo layout: `engine/` (Go sync core, scheduler, backends, CLI), `hash/`
      (Rust BLAKE3 helper binary crate), `src-tauri/` (Tauri backend), `src/`
      (TS/Vite frontend, no framework), shared module/workspace metadata
      (`license`, `authors = ["TPT Solutions"]`, `repository`)
- [x] `go.mod` module setup, `rust-toolchain.toml` (MSRV pin), `rustfmt.toml`, `clippy.toml`
- [x] Configure `tauri.conf.json` (app id `com.tptsolutions.tpt-sync`, window defaults, bundle metadata)
      — `src-tauri/tauri.conf.json`. Bundle identifier is a placeholder pending
      confirmation of the TPT Solutions reverse-DNS prefix.
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
- [x] Benchmarks: throughput vs. a pure-Go BLAKE3 baseline — `engine/hasher_bench_test.go`.
      Result (3 iterations, release helper, this machine): 64 MiB single file,
      helper 1,432 MB/s vs pure-Go 1,895 MB/s, so the subprocess is slower for a
      large single file. Batching is what pays off: 1,000 small files in one
      helper call took 2.2 s versus about 17 ms per file when spawned per file.
      Open: Rust-side multithreading (blake3 rayon feature) might close the gap;
      large-tree benchmarks not yet run.
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
      — `engine/backend/webdav.go`. Tested against an in-memory fake and against
      `golang.org/x/net/webdav`, the Go team's server. The real-server tests
      found and fixed two bugs: nested folders were not created, and parallel
      uploads raced to create the same collection. Not yet tested against
      Nextcloud, Apache mod_dav, or a NAS.
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
- [x] Missed-run handling: per-job `missed_run` = `catch-up` (default, runs once
      at startup) or `skip` (waits for the next tick). — `engine/job.go`,
      `--missed-run`. "On next wake" is covered by catch-up at startup; OS wake
      events are not hooked.
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
- [x] CLI talks to the same engine/daemon used by the GUI (local
      socket/named pipe IPC). `run` delegates to a running daemon so manual and
      scheduled runs never overlap; `status` shows live RUNNING state.
      — `engine/ipc/` (named pipe on Windows, user-only Unix socket elsewhere),
      `engine/daemon/` control handler (ping, status, run).
      Not yet: live reload of job edits made while the daemon runs (restart it).
- [x] CLI unit/integration tests — `engine/cmd/tpt-sync/main_test.go` covers
      job lifecycle, dry-run, real runs, restore, usage errors, remote type
      inference, and delegation to a running daemon.

## Phase 9 — Tauri Tray/GUI Status App
- [~] Tray icon + menu: job status at a glance, quick run/pause, open main window
      — done: tray icon with Show/Quit, left-click opens the window, close hides to tray.
      Pending: per-job status in the tray menu, pause.
- [~] Main window: job list, per-job status/progress, last-run history
      — done: job list with live RUNNING state, last run result, and Run now.
      Pending: per-file progress (the daemon does not stream progress yet).
- [ ] Job editor: source/destination pickers (incl. cloud OAuth connect flow),
      schedule config, versioning/retention settings, encryption toggle
- [ ] Version browser/restore UI (Phase 5 restore flow)
- [ ] Settings: autostart toggle, global concurrency/bandwidth limits,
      light/dark theme
- [x] IPC between the Go scheduler/engine daemon and the Tauri backend
      (named pipe on Windows, user socket elsewhere), using the same control
      channel as the CLI. `src-tauri/src/ipc.rs` mirrors the Go endpoint naming,
      and a test pins the Windows pipe name to the Go implementation's output.

## Phase 10 — Linux Filesystem-Specific Support
- [ ] Native ext4/btrfs/zfs support: verify hardlink/versioning behavior per
      filesystem, detect filesystem type to choose the right versioning strategy
- [ ] btrfs/zfs snapshot integration as an *optional* faster-path alternative
      to hardlink dedup when the destination filesystem supports native
      snapshots (still exposed through the same versioning UI/CLI)
- [x] Linux packaging groundwork: systemd user unit with restart and sandboxing
      — `packaging/systemd/tpt-sync.service` (not yet run on a Linux host)
- [ ] Verify behavior on at least one ext4 and one btrfs/zfs test target

## Phase 11 — Archon Support (Design Only)
- [x] `docs/archon-design.md`: design for zero-copy, space-efficient local
      versioning via B-Link tree deltas through `tpt-archon-core`, enabling
      instantaneous local snapshots before syncing to external targets
- [x] Open Questions (pending tpt-archon team): recorded in
      `docs/archon-design.md` (delta API shape, mapping to versioning model,
      file addressing, hardlink fallback interaction, consistency, encryption)
- [x] Post-v1 `archon` store stub: selecting `versioning.store = "archon"`
      fails validation with a pointer to the design, rather than falling back.
      — `engine/job.go`, tested in `engine/job_test.go`. No `Available()`
      backend yet; that waits on the tpt-archon API.

## Phase 12 — Performance & Concurrency Polish
- [~] Large-tree performance tuning (millions of small files scenario):
      walker/diffing throughput, worker pool tuning.
      Measured 100,000 files in 100 folders (`TPT_SYNC_STRESS=100000`, this
      Windows machine, default 4 workers):
        - first run, copies everything: 12 min 14 s
        - no-change run, hardlinks everything: 47.6 s
        - heap after first run: 59 MiB
      Finding: the first run is dominated by the per-file fsync that makes each
      file durable before the snapshot is published. Measured alone, one fsync
      costs about 6.4 ms here versus 0.8 ms for the write without it, and four
      workers do not help because NTFS serialises the flushes. Decision needed:
      keep per-file fsync (current, safe), or drop it for a faster mode that can
      publish a snapshot whose files are not yet on disk after a power loss.
- [x] Bandwidth throttling: per job (shared by the job's transfers) and daemon-wide
      (`tpt-sync daemon --bandwidth-limit`, shared across all jobs). Both apply
      to every transfer. — `engine/throttle.go`
      Not yet: a cap on the CLI's in-process runs when no daemon is running.
- [ ] Memory profiling (engine daemon, checksum helper batching, WebView)
- [ ] Resumable transfer correctness under interruption (network drop,
      process kill) across all backends
- [ ] App icon, branding assets (TPT Solutions)

## Phase 13 — Testing & QA
- [~] Unit tests: diffing, versioning/dedup, scheduling, encryption (per-phase
      tests in place; consolidated coverage pass pending)
- [x] Fuzz targets: filter matching, size parsing, cron parsing, stream
      decryption. A 15-20 s run of each found no panics; one size-overflow bug
      was found and fixed.
- [x] Daemon and CLI tests over real transports (named pipe on Windows).
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
