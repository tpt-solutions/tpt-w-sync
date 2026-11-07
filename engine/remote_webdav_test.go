// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"github.com/tpt-solutions/tpt-sync/engine/backend"
)

// The Go team's WebDAV server (golang.org/x/net/webdav) backed by a directory.
// Unlike the in-memory fake used elsewhere, it is a complete implementation,
// so these tests exercise the client against a server that behaves like a
// real one: Depth handling, collection hrefs, MKCOL conflicts, and locking.
func realWebDAV(t *testing.T) (*backend.WebDAV, string) {
	t.Helper()
	root := t.TempDir()
	h := &webdav.Handler{
		Prefix:     "/dav",
		FileSystem: webdav.Dir(root),
		LockSystem: webdav.NewMemLS(),
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	w, err := backend.NewWebDAV(backend.WebDAVConfig{
		BaseURL: srv.URL + "/dav", Folder: "backups/pc1", AllowInsecureHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return w, root
}

// Full cycle against a real WebDAV server: versioned runs, a change, prune by
// count (which deletes collections on the server), restore from an older
// snapshot, and an encrypted run on the same server.
func TestRealWebDAVFullCycle(t *testing.T) {
	dst, root := realWebDAV(t)
	job, src, _ := newTestJob(t)
	job.Versioning.MaxVersions = 2
	writeFile(t, src, "docs/a.txt", "alpha", base0)
	writeFile(t, src, "b.txt", "v1", base0)
	ctx := context.Background()
	clk := &fixedClock{t: base0}
	opts := Options{Hasher: &shaHasher{}, Now: clk.now, Backend: dst}

	var snaps []string
	for i, content := range []string{"v1", "v2-longer", "v3-longest-content"} {
		writeFile(t, src, "b.txt", content, base0.Add(time.Duration(i+1)*time.Hour))
		res, err := Run(ctx, job, opts)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		snaps = append(snaps, res.Snapshot)
	}

	// Retention pruned the first snapshot on the server, not just in the list.
	names, err := ListSnapshots(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != snaps[1] || names[1] != snaps[2] {
		t.Fatalf("server snapshots = %v, want %v", names, snaps[1:])
	}
	if _, err := os.Stat(filepath.Join(root, "backups", "pc1", ".tpt-sync", "versions", snaps[0])); !os.IsNotExist(err) {
		t.Fatal("pruned snapshot still present on the server")
	}

	// Restore the older kept snapshot: content must be the version from that run.
	out := t.TempDir()
	if _, err := Restore(ctx, job, RestoreOptions{Snapshot: snaps[1], Backend: dst}, out); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(out, "b.txt")); got != "v2-longer" {
		t.Fatalf("restored %q, want v2-longer", got)
	}
	if got := readFile(t, filepath.Join(out, "docs", "a.txt")); got != "alpha" {
		t.Fatalf("restored nested file %q", got)
	}
}

// An encrypted job on a real WebDAV server: nothing readable reaches the
// server's storage, and a restore with the passphrase reproduces the data.
func TestRealWebDAVEncrypted(t *testing.T) {
	dst, root := realWebDAV(t)
	job, src, _ := newTestJob(t)
	job.Encrypt = true
	secret := "board minutes, do not share"
	writeFile(t, src, "minutes/2026.txt", secret, base0)
	ctx := context.Background()
	res, err := Run(ctx, job, Options{
		Hasher: &shaHasher{}, Now: (&fixedClock{t: base0}).now, Backend: dst,
		Passphrase: []byte("long passphrase for the test"),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.Contains(p, "minutes") || strings.HasSuffix(p, "2026.txt") {
			t.Errorf("plaintext name on server: %s", p)
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), secret) {
			t.Errorf("plaintext content on server: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if _, err := Restore(ctx, job, RestoreOptions{
		Snapshot: res.Snapshot, Backend: dst, Passphrase: []byte("long passphrase for the test"),
	}, out); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(out, "minutes", "2026.txt")); got != secret {
		t.Fatalf("restored %q", got)
	}
}
