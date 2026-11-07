// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine/backend"
	"github.com/tpt-solutions/tpt-sync/engine/crypt"
	"github.com/tpt-solutions/tpt-sync/engine/secrets"
	"golang.org/x/time/rate"
)

// Progress is reported once per completed action during execution.
type Progress struct {
	Action Action
	Done   int
	Total  int
	Err    error
}

// Options tune a single run.
type Options struct {
	// Hasher computes checksums; nil uses the Rust helper (ExecHasher).
	Hasher Hasher
	// DryRun plans but does not touch the destination.
	DryRun bool
	// Now supplies the clock; nil uses time.Now.
	Now func() time.Time
	// OnProgress is called from worker goroutines; may be nil.
	OnProgress func(Progress)
	// Passphrase overrides the stored passphrase for encrypted jobs. When nil,
	// it is read from Secrets.
	Passphrase []byte
	// Secrets supplies passphrases and remote destination passwords. May be
	// nil for local, unencrypted jobs.
	Secrets secrets.Store
	// Backend overrides the destination built from job.Destination. Used by
	// tests and by callers that already hold an open backend.
	Backend backend.Backend
	// SharedLimiter, if set, caps this run together with other runs that share
	// it (see NewSharedLimiter). It applies in addition to the job's own limit.
	SharedLimiter *rate.Limiter
}

// Result summarises a run.
type Result struct {
	Plan     Plan
	Snapshot string // empty for dry runs
	Copied   int
	Linked   int
	Deleted  int
	Bytes    int64
	DryRun   bool
}

// Run executes one sync of job into a new snapshot on the destination.
func Run(ctx context.Context, job Job, opts Options) (Result, error) {
	if err := job.Validate(); err != nil {
		return Result{}, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Hasher == nil {
		opts.Hasher = ExecHasher{}
	}

	srcAbs, err := filepath.Abs(job.Source)
	if err != nil {
		return Result{}, err
	}
	if fi, err := os.Stat(srcAbs); err != nil || !fi.IsDir() {
		return Result{}, fmt.Errorf("source %q is not an existing directory", job.Source)
	}
	src, err := backend.NewLocal(srcAbs)
	if err != nil {
		return Result{}, err
	}

	dst := opts.Backend
	if dst == nil {
		if dst, err = OpenDestination(job, opts.Secrets); err != nil {
			return Result{}, err
		}
	}
	local, isLocal := dst.(*backend.Local)
	if isLocal && within(local.Root(), srcAbs) {
		return Result{}, errors.New("destination must not be inside the source directory")
	}

	srcFiles, err := scanFiltered(ctx, src, job.Filters)
	if err != nil {
		return Result{}, fmt.Errorf("scanning source: %w", err)
	}

	encPresent, err := hasEncryptionMetadata(ctx, dst)
	if err != nil {
		return Result{}, err
	}
	if !job.Encrypt && encPresent {
		return Result{}, errors.New("destination is encrypted; enable encryption for this job or use a different destination")
	}
	var keys *crypt.Keys
	if job.Encrypt && !(opts.DryRun && !encPresent) {
		pass, err := resolvePassphrase(job, opts.Passphrase, opts.Secrets)
		if err != nil {
			return Result{}, err
		}
		if keys, err = openKeys(ctx, dst, pass, true); err != nil {
			return Result{}, err
		}
	}

	latest, err := ReadLatest(ctx, dst)
	if err != nil {
		return Result{}, err
	}
	prevFiles := map[string]backend.FileInfo{}
	prevDir := ""
	prevRoot := ""
	if latest != "" {
		prevDir = snapshotDir(latest)
		if job.Encrypt && keys == nil {
			return Result{}, errors.New("destination has snapshots but no encryption metadata")
		}
		m, err := readManifest(ctx, dst, keys, prevDir)
		if err != nil {
			return Result{}, fmt.Errorf("previous snapshot %s: %w", latest, err)
		}
		for _, e := range m.Entries {
			prevFiles[e.Path] = backend.FileInfo{Path: e.Path, Size: e.Size, ModTime: e.ModTime}
		}
		// Content comparison needs readable local plaintext of the previous
		// snapshot; otherwise changed-mtime files are copied conservatively.
		if isLocal && !job.Encrypt {
			prevRoot = filepath.Join(local.Root(), filepath.FromSlash(prevDir))
		}
	}

	plan, err := BuildPlan(ctx, srcFiles, prevFiles, src.Root(), prevRoot, opts.Hasher)
	if err != nil {
		return Result{}, fmt.Errorf("planning: %w", err)
	}
	res := Result{Plan: plan, DryRun: opts.DryRun}
	if opts.DryRun {
		return res, nil
	}

	ts := newSnapshotName(opts.Now(), latest)
	snap := snapshotDir(ts)
	if err := ctx.Err(); err != nil {
		return res, err
	}

	ex := executor{
		src: src, dst: dst, snap: snap, prevDir: prevDir, keys: keys,
		limiter:  newLimiter(job.BandwidthLimit),
		shared:   opts.SharedLimiter,
		hardlink: dst.Capabilities().Hardlink,
		progress: opts.OnProgress,
		total:    len(plan.Actions),
	}
	copied, linked, deleted, bytes, err := ex.run(ctx, plan, job.concurrency())
	res.Copied, res.Linked, res.Deleted, res.Bytes = copied, linked, deleted, bytes
	if err == nil {
		err = writeManifest(ctx, dst, keys, snap, manifestFromPlan(plan, keys))
	}
	if err != nil {
		// Discard the partial snapshot; latest still points at the last good one.
		_ = dst.RemoveTree(context.WithoutCancel(ctx), snap)
		return res, err
	}

	res.Snapshot = ts
	if err := writeLatest(ctx, dst, ts); err != nil {
		return res, err
	}
	if _, err := Prune(ctx, dst, job.Versioning, opts.Now(), ts); err != nil {
		return res, fmt.Errorf("retention: %w", err)
	}
	return res, nil
}

// executor applies a plan into a snapshot directory using a bounded worker pool.
type executor struct {
	src      *backend.Local
	dst      backend.Backend
	snap     string        // destination snapshot dir, e.g. .tpt-sync/versions/<ts>
	prevDir  string        // previous snapshot dir, "" if none
	keys     *crypt.Keys   // nil for plaintext destinations
	limiter  *rate.Limiter // this job's cap, shared by its transfers; nil = unlimited
	shared   *rate.Limiter // caller-supplied cap shared across runs; nil = none
	hardlink bool
	progress func(Progress)
	total    int

	mu      sync.Mutex
	done    int
	copied  int
	linked  int
	deleted int
	bytes   int64
}

func (e *executor) run(ctx context.Context, plan Plan, workers int) (copied, linked, deleted int, bytes int64, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan Action)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range work {
				aerr := e.apply(ctx, a)
				e.report(a, aerr)
				if aerr != nil {
					errOnce.Do(func() {
						firstErr = fmt.Errorf("%s: %w", a.Path, aerr)
						cancel()
					})
				}
			}
		}()
	}

feed:
	for _, a := range plan.Actions {
		if a.Kind == KindDelete {
			e.mu.Lock()
			e.deleted++
			e.mu.Unlock()
			continue
		}
		select {
		case work <- a:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()
	if firstErr != nil {
		return e.copied, e.linked, e.deleted, e.bytes, firstErr
	}
	if err := ctx.Err(); err != nil {
		return e.copied, e.linked, e.deleted, e.bytes, err
	}
	return e.copied, e.linked, e.deleted, e.bytes, nil
}

func (e *executor) apply(ctx context.Context, a Action) error {
	name := storedName(e.keys, a.Path)
	dstRel := path.Join(e.snap, name)
	if a.Kind == KindUnchanged && e.hardlink && e.prevDir != "" {
		err := e.dst.Link(ctx, path.Join(e.prevDir, name), dstRel)
		if err == nil {
			e.mu.Lock()
			e.linked++
			e.mu.Unlock()
			return nil
		}
		// Fall through to a copy if linking failed for any reason.
	}
	r, err := e.src.Open(ctx, a.Path)
	if err != nil {
		return err
	}
	defer r.Close()
	src := throttle(ctx, throttle(ctx, r, e.limiter), e.shared)
	if e.keys == nil {
		err = e.dst.Write(ctx, dstRel, src, a.ModTime)
	} else {
		err = e.writeEncrypted(ctx, dstRel, src, a.ModTime)
	}
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.copied++
	e.bytes += a.Size
	e.mu.Unlock()
	return nil
}

// writeEncrypted streams plaintext through the cipher into the destination
// without buffering whole files. The pipe is closed with the write's error, so
// a failed write also stops the encrypting goroutine.
func (e *executor) writeEncrypted(ctx context.Context, dstRel string, r io.Reader, mt time.Time) error {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(e.keys.EncryptStream(pw, r))
	}()
	err := e.dst.Write(ctx, dstRel, pr, mt)
	pr.CloseWithError(err)
	return err
}

// manifestFromPlan lists every file present in the new snapshot.
func manifestFromPlan(plan Plan, keys *crypt.Keys) []manifestEntry {
	entries := make([]manifestEntry, 0, len(plan.Actions))
	for _, a := range plan.Actions {
		if a.Kind == KindDelete {
			continue
		}
		entries = append(entries, manifestEntry{
			Path: a.Path, Stored: storedName(keys, a.Path), Size: a.Size, ModTime: a.ModTime,
		})
	}
	return entries
}

func (e *executor) report(a Action, err error) {
	e.mu.Lock()
	e.done++
	p := Progress{Action: a, Done: e.done, Total: e.total, Err: err}
	e.mu.Unlock()
	if e.progress != nil {
		e.progress(p)
	}
}

// ReadLatest returns the name of the newest complete snapshot, or "" if none.
func ReadLatest(ctx context.Context, dst backend.Backend) (string, error) {
	r, err := dst.Open(ctx, latestFile)
	if errors.Is(err, backend.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func writeLatest(ctx context.Context, dst backend.Backend, ts string) error {
	return dst.Write(ctx, latestFile, strings.NewReader(ts+"\n"), time.Now())
}

// ListSnapshots returns snapshot names, oldest first.
func ListSnapshots(ctx context.Context, dst backend.Backend) ([]string, error) {
	return dst.ListDirs(ctx, versionsDir)
}

// Prune removes snapshots that exceed the job's retention policy. The snapshot
// named keep is never removed. Returns the names removed.
func Prune(ctx context.Context, dst backend.Backend, v Versioning, now time.Time, keep string) ([]string, error) {
	names, err := ListSnapshots(ctx, dst)
	if err != nil {
		return nil, err
	}
	// names is oldest-first; everything before cutoff falls outside the
	// newest MaxVersions snapshots.
	cutoff := 0
	if v.MaxVersions > 0 && len(names) > v.MaxVersions {
		cutoff = len(names) - v.MaxVersions
	}
	var removed []string
	for i, name := range names {
		if name == keep {
			continue
		}
		tooMany := i < cutoff
		tooOld := false
		if v.MaxAge > 0 {
			if t, err := time.ParseInLocation(tsLayout, name, time.UTC); err == nil {
				tooOld = now.Sub(t) > v.MaxAge
			}
		}
		if !tooOld && !tooMany {
			continue
		}
		if err := dst.RemoveTree(ctx, snapshotDir(name)); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// RestoreOptions selects what to restore from a snapshot.
type RestoreOptions struct {
	// Snapshot is the snapshot name; "" means the latest.
	Snapshot string
	// Path is a slash-relative file or directory within the snapshot; "" = all.
	Path string
	// Passphrase overrides the stored passphrase for encrypted jobs.
	Passphrase []byte
	// Secrets supplies passphrases and remote destination passwords.
	Secrets secrets.Store
	// Backend overrides the destination built from the job.
	Backend backend.Backend
}

// Restore copies files from a snapshot into destDir, preserving relative
// layout and modification times. Returns the number of files restored.
func Restore(ctx context.Context, job Job, opts RestoreOptions, destDir string) (int, error) {
	dst := opts.Backend
	if dst == nil {
		var err error
		if dst, err = OpenDestination(job, opts.Secrets); err != nil {
			return 0, err
		}
	}
	snap := opts.Snapshot
	if snap == "" {
		var err error
		if snap, err = ReadLatest(ctx, dst); err != nil {
			return 0, err
		}
		if snap == "" {
			return 0, errors.New("no snapshots exist for this job")
		}
	}
	var keys *crypt.Keys
	if job.Encrypt {
		pass, err := resolvePassphrase(job, opts.Passphrase, opts.Secrets)
		if err != nil {
			return 0, err
		}
		if keys, err = openKeys(ctx, dst, pass, false); err != nil {
			return 0, err
		}
	}
	base := snapshotDir(snap)
	m, err := readManifest(ctx, dst, keys, base)
	if err != nil {
		return 0, err
	}
	out, err := backend.NewLocal(destDir)
	if err != nil {
		return 0, err
	}
	rel := path.Clean(strings.TrimPrefix(opts.Path, "/"))
	if rel == "." {
		rel = ""
	}

	n := 0
	for _, e := range m.Entries {
		if rel != "" && e.Path != rel && !strings.HasPrefix(e.Path, rel+"/") {
			continue
		}
		if err := restoreEntry(ctx, dst, out, keys, base, e); err != nil {
			return n, fmt.Errorf("restoring %s: %w", e.Path, err)
		}
		n++
	}
	if n == 0 && rel != "" {
		return 0, fmt.Errorf("%q not found in snapshot %s", rel, snap)
	}
	return n, nil
}

// restoreEntry copies one manifest entry from the snapshot into out. Output
// is written atomically, so a failed authentication leaves no partial file.
func restoreEntry(ctx context.Context, dst backend.Backend, out backend.Backend, keys *crypt.Keys, base string, e manifestEntry) error {
	r, err := dst.Open(ctx, base+"/"+e.Stored)
	if err != nil {
		return err
	}
	defer r.Close()
	if keys == nil {
		return out.Write(ctx, e.Path, r, e.ModTime)
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(keys.DecryptStream(pw, r))
	}()
	err = out.Write(ctx, e.Path, pr, e.ModTime)
	pr.CloseWithError(err)
	return err
}

// scanFiltered walks a source backend and returns files passing the filters.
// The .tpt-sync metadata directory is always skipped.
func scanFiltered(ctx context.Context, b backend.Backend, f Filters) (map[string]backend.FileInfo, error) {
	out := map[string]backend.FileInfo{}
	err := b.Walk(ctx, "", func(fi backend.FileInfo) error {
		if fi.Path == metaDir || strings.HasPrefix(fi.Path, metaDir+"/") {
			return nil
		}
		if f.Match(fi.Path) {
			out[fi.Path] = fi
		}
		return nil
	})
	return out, err
}

// resolvePassphrase returns the explicit passphrase if given, else the one
// stored for the job. Returns a clear error when neither exists.
func resolvePassphrase(job Job, explicit []byte, store secrets.Store) ([]byte, error) {
	if explicit != nil {
		return explicit, nil
	}
	if store != nil {
		v, err := store.Get(secrets.PassphraseAccount(job.Name))
		if err == nil {
			return []byte(v), nil
		}
		if !errors.Is(err, secrets.ErrNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no passphrase for encrypted job %q; run: tpt-sync secret set %s passphrase",
		job.Name, job.Name)
}

func snapshotDir(ts string) string { return versionsDir + "/" + ts }

// newSnapshotName returns a UTC timestamp name strictly newer than prev, so
// snapshot names sort chronologically even if the clock stalls.
func newSnapshotName(now time.Time, prev string) string {
	t := now.UTC()
	if prev != "" {
		if pt, err := time.ParseInLocation(tsLayout, prev, time.UTC); err == nil && !t.After(pt) {
			t = pt.Add(time.Nanosecond)
		}
	}
	return t.Format(tsLayout)
}

// within reports whether p is equal to or inside dir (both absolute).
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
