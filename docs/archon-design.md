# Archon Support — Design (Phase 11, design only)

Status: **design only**. No Archon code ships in v1. This document records the
intended integration, what it depends on, and the questions that must be
answered by the tpt-archon team before implementation starts.

Reference: <https://github.com/tpt-solutions/tpt-archon>

## Goal

On Archon, create instantaneous, space-efficient local snapshots as
zero-copy B-Link tree deltas via `tpt-archon-core`, then sync those snapshots
to external targets (local drives, SMB, WebDAV, S3, cloud) using the normal
sync engine.

Local versioning becomes a *fast local tier* in front of the existing
external-destination path. The external path does not change.

## Where it fits in the current model

The v1 engine already separates **what a snapshot is** from **how it is
stored**:

| Concern | v1 implementation | Archon equivalent |
|---|---|---|
| Snapshot identity | timestamped `.tpt-sync/versions/<ts>/` | Archon snapshot ID (B-Link root) |
| Unchanged-data dedup | hardlinks to previous snapshot (`Backend.Link`) | shared B-Link subtrees (structural sharing) |
| Change detection | size + mtime, BLAKE3 on mismatch | tree diff between roots (delta) |
| Retention | `Prune` by count / age | Archon snapshot release |
| Restore | copy a file/tree out of a snapshot | materialise a path from a root |

The Archon backend therefore plugs in as a **snapshot store**, not as a file
destination. The engine asks it for: create snapshot from a source tree,
list snapshots, diff two snapshots, read a path from a snapshot, and release
a snapshot.

## Proposed interface (Go, post-v1)

```go
// engine/backend/archon (planned)
type SnapshotStore interface {
    Available() bool                                   // false until Archon runtime detected
    Create(ctx context.Context, src string) (SnapshotID, error)
    List(ctx context.Context) ([]SnapshotID, error)
    Diff(ctx context.Context, from, to SnapshotID) ([]Change, error)
    Open(ctx context.Context, id SnapshotID, rel string) (io.ReadCloser, error)
    Release(ctx context.Context, id SnapshotID) error
}
```

`Diff` replaces the filesystem scan + planner for Archon-backed jobs when both
endpoints are Archon snapshots. The planner's output (`Plan`) is unchanged, so
the executor and external backends are reused as-is.

## Post-v1 stub (planned, low risk)

Before the real integration, ship a stub that keeps the code path honest:

- `Available()` returns `false` on every platform.
- Every other method returns `ErrUnsupported`.
- Jobs with `"versioning": {"store": "archon"}` fail validation with a clear
  message when Archon is unavailable, instead of silently falling back.

This lets the configuration surface and the CLI/GUI wording be finalised
without depending on the Archon API.

## Open questions (for the tpt-archon team)

1. **Delta API shape.** Is the diff exposed as a streaming iterator of
   `{path, kind, oldID, newID}`, or as a full materialised list? Large trees
   (100k+ files) need streaming.
2. **Mapping to the versioning model.** Should an Archon snapshot be one-to-one
   with a tpt-sync version (`<ts>`), or can several Archon snapshots collapse
   into one sync version? Retention policy must be defined in one place.
3. **File-level addressing.** Are paths first-class keys in the B-Link tree, or
   are file contents addressed separately (content-addressed blobs under a
   path index)? This decides whether path-level restore is cheap.
4. **Interaction with hardlink dedup.** For non-Archon destinations the v1
   hardlink fallback still applies. Confirm that an Archon snapshot can be
   exported as a full logical tree (so S3/WebDAV receive normal copies) without
   re-reading the whole tree from disk.
5. **Consistency.** Is `Create` atomic with respect to concurrent writes to the
   source? If not, the engine needs a quiesce step or must record that a
   snapshot may be torn.
6. **Encryption (Phase 7) interaction.** Encrypted destinations depend on
   plaintext being streamed through the cipher. Confirm that exporting from an
   Archon snapshot yields a stream, not a requirement to materialise it.

## Non-goals for v1

- No Archon dependency in the default build.
- No change to the on-destination layout for non-Archon destinations.
- No attempt to make Archon snapshots readable by other tools.
