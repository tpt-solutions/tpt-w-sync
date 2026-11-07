// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine/backend"
)

// shaHasher is an in-process Hasher so engine tests do not depend on the Rust
// helper binary. It records how many paths it was asked to hash.
type shaHasher struct{ calls int }

func (h *shaHasher) Hash(_ context.Context, paths []string) ([]HashResult, error) {
	out := make([]HashResult, len(paths))
	for i, p := range paths {
		h.calls++
		b, err := os.ReadFile(p)
		if err != nil {
			out[i] = HashResult{Path: p, Err: err}
			continue
		}
		s := sha256.Sum256(b)
		out[i] = HashResult{Path: p, Sum: hex.EncodeToString(s[:])}
	}
	return out, nil
}

// fixedClock returns successive times one second apart, so snapshot names are
// deterministic and ordered.
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time { c.t = c.t.Add(time.Second); return c.t }

func writeFile(t *testing.T, root, rel, content string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newTestJob(t *testing.T) (Job, string, string) {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	job := Job{
		Name:        "t",
		Source:      src,
		Destination: Destination{Type: "local", Path: dst},
		Enabled:     true,
	}
	return job, src, dst
}

var base0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestFirstRunCopiesEverything(t *testing.T) {
	job, src, dst := newTestJob(t)
	writeFile(t, src, "a.txt", "alpha", base0)
	writeFile(t, src, "sub/b.txt", "beta", base0)

	clk := &fixedClock{t: base0}
	res, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	if res.Copied != 2 || res.Linked != 0 {
		t.Fatalf("copied=%d linked=%d, want 2/0", res.Copied, res.Linked)
	}
	if got := readFile(t, filepath.Join(dst, ".tpt-sync", "versions", res.Snapshot, "sub", "b.txt")); got != "beta" {
		t.Fatalf("snapshot content = %q", got)
	}
}

func TestUnchangedFilesAreHardlinkedAndChangesVersioned(t *testing.T) {
	job, src, dst := newTestJob(t)
	writeFile(t, src, "same.txt", "unchanged", base0)
	writeFile(t, src, "edit.txt", "v1", base0)
	writeFile(t, src, "gone.txt", "bye", base0)

	clk := &fixedClock{t: base0}
	ctx := context.Background()
	r1, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}

	// Change one file (new content, new size), delete one, leave one alone.
	writeFile(t, src, "edit.txt", "version-two", base0.Add(time.Hour))
	if err := os.Remove(filepath.Join(src, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	r2, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Linked != 1 || r2.Copied != 1 || r2.Deleted != 1 {
		t.Fatalf("run2 linked=%d copied=%d deleted=%d, want 1/1/1", r2.Linked, r2.Copied, r2.Deleted)
	}

	old1 := filepath.Join(dst, ".tpt-sync", "versions", r1.Snapshot)
	new2 := filepath.Join(dst, ".tpt-sync", "versions", r2.Snapshot)

	// Unchanged file shares an inode with the previous snapshot.
	a, _ := os.Stat(filepath.Join(old1, "same.txt"))
	b, _ := os.Stat(filepath.Join(new2, "same.txt"))
	if !os.SameFile(a, b) {
		t.Fatal("unchanged file is not hardlinked between snapshots")
	}
	// Changed file: old version preserved, new version present.
	if got := readFile(t, filepath.Join(old1, "edit.txt")); got != "v1" {
		t.Fatalf("old snapshot edit.txt = %q, want v1", got)
	}
	if got := readFile(t, filepath.Join(new2, "edit.txt")); got != "version-two" {
		t.Fatalf("new snapshot edit.txt = %q", got)
	}
	// Deleted file: absent from latest, still present in the old snapshot.
	if _, err := os.Stat(filepath.Join(new2, "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("deleted file should not appear in the new snapshot")
	}
	if got := readFile(t, filepath.Join(old1, "gone.txt")); got != "bye" {
		t.Fatalf("deleted file lost from old snapshot: %q", got)
	}
}

func TestSameSizeDifferentMtimeResolvedByChecksum(t *testing.T) {
	job, src, _ := newTestJob(t)
	writeFile(t, src, "f.txt", "aaaa", base0)
	clk := &fixedClock{t: base0}
	ctx := context.Background()
	if _, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now}); err != nil {
		t.Fatal(err)
	}

	// Touch the mtime only: content identical → should be unchanged, not copied.
	writeFile(t, src, "f.txt", "aaaa", base0.Add(time.Hour))
	h := &shaHasher{}
	res, err := Run(ctx, job, Options{Hasher: h, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	if h.calls == 0 {
		t.Fatal("expected checksum resolution for same-size, different-mtime file")
	}
	if res.Copied != 0 || res.Linked != 1 {
		t.Fatalf("copied=%d linked=%d, want 0/1", res.Copied, res.Linked)
	}

	// Same size, different content, different mtime → must be updated.
	writeFile(t, src, "f.txt", "bbbb", base0.Add(2*time.Hour))
	res, err = Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	if res.Copied != 1 {
		t.Fatalf("content change not detected: copied=%d", res.Copied)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	job, src, dst := newTestJob(t)
	writeFile(t, src, "a.txt", "x", base0)
	res, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Snapshot != "" {
		t.Fatalf("dry run result = %+v", res)
	}
	if res.Plan.Counts()[KindCreate] != 1 {
		t.Fatalf("plan counts = %v", res.Plan.Counts())
	}
	if _, err := os.Stat(filepath.Join(dst, ".tpt-sync")); !os.IsNotExist(err) {
		t.Fatal("dry run created destination metadata")
	}
}

func TestFiltersExcludeFromSnapshot(t *testing.T) {
	job, src, dst := newTestJob(t)
	job.Filters = Filters{Exclude: []string{"*.tmp", "cache/**"}}
	writeFile(t, src, "keep.txt", "k", base0)
	writeFile(t, src, "junk.tmp", "t", base0)
	writeFile(t, src, "cache/deep/x.bin", "c", base0)
	res, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}, Now: (&fixedClock{t: base0}).now})
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dst, ".tpt-sync", "versions", res.Snapshot)
	if _, err := os.Stat(filepath.Join(snap, "keep.txt")); err != nil {
		t.Fatal("keep.txt should be synced")
	}
	for _, rel := range []string{"junk.tmp", "cache/deep/x.bin"} {
		if _, err := os.Stat(filepath.Join(snap, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s should be excluded", rel)
		}
	}
}

func TestRetentionKeepsNewestAndLatest(t *testing.T) {
	job, src, dst := newTestJob(t)
	job.Versioning = Versioning{MaxVersions: 2}
	writeFile(t, src, "a.txt", "1", base0)
	clk := &fixedClock{t: base0}
	ctx := context.Background()
	var names []string
	for i := 0; i < 4; i++ {
		writeFile(t, src, "a.txt", string(rune('1'+i))+"x", base0.Add(time.Duration(i+1)*time.Hour))
		res, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, res.Snapshot)
	}
	dstB, _ := backend.NewLocal(dst)
	got, err := ListSnapshots(ctx, dstB)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != names[2] || got[1] != names[3] {
		t.Fatalf("retained %v, want %v", got, names[2:])
	}
	latest, _ := ReadLatest(ctx, dstB)
	if latest != names[3] {
		t.Fatalf("latest = %s, want %s", latest, names[3])
	}
}

func TestRestoreFromSnapshot(t *testing.T) {
	job, src, _ := newTestJob(t)
	writeFile(t, src, "d/a.txt", "hello", base0)
	writeFile(t, src, "b.txt", "world", base0)
	clk := &fixedClock{t: base0}
	ctx := context.Background()
	res, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	n, err := Restore(ctx, job, RestoreOptions{Snapshot: res.Snapshot, Path: "d"}, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || readFile(t, filepath.Join(out, "d", "a.txt")) != "hello" {
		t.Fatalf("restored %d files, content mismatch", n)
	}
	n, err = Restore(ctx, job, RestoreOptions{}, t.TempDir())
	if err != nil || n != 2 {
		t.Fatalf("restore latest all: n=%d err=%v", n, err)
	}
}

func TestDestinationInsideSourceRejected(t *testing.T) {
	job, src, _ := newTestJob(t)
	job.Destination.Path = filepath.Join(src, "backup")
	if _, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}}); err == nil {
		t.Fatal("expected error for destination inside source")
	}
}

func TestFailedCopyLeavesLatestUnchanged(t *testing.T) {
	job, src, dst := newTestJob(t)
	writeFile(t, src, "a.txt", "one", base0)
	clk := &fixedClock{t: base0}
	ctx := context.Background()
	r1, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}

	// Cancel mid-run: the context is already done, so the run must fail and
	// must not advance latest.
	writeFile(t, src, "a.txt", "two-two", base0.Add(time.Hour))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Run(cctx, job, Options{Hasher: &shaHasher{}, Now: clk.now}); err == nil {
		t.Fatal("expected cancelled run to fail")
	}
	dstB, _ := backend.NewLocal(dst)
	latest, _ := ReadLatest(ctx, dstB)
	if latest != r1.Snapshot {
		t.Fatalf("latest moved to %s after failed run", latest)
	}
}
