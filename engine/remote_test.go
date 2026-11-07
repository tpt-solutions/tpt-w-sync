// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/tpt-solutions/tpt-sync/engine/backend"
	"github.com/tpt-solutions/tpt-sync/engine/internal/davfake"
	"github.com/tpt-solutions/tpt-sync/engine/secrets"
)

// remoteS3 starts an in-memory S3 over TLS and returns a backend for it.
func remoteS3(t *testing.T, prefix string) *backend.S3 {
	t.Helper()
	mem := s3mem.New()
	if err := mem.CreateBucket("sync"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewTLSServer(gofakes3.New(mem).Server())
	t.Cleanup(ts.Close)
	s, err := backend.NewS3(backend.S3Config{
		Endpoint: strings.TrimPrefix(ts.URL, "https://"), Secure: true, Region: "us-east-1",
		Bucket: "sync", Prefix: prefix, AccessKey: "AK", SecretKey: "SK",
		Transport: ts.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A full versioned cycle over WebDAV, through the job's own destination config.
// WebDAV has no hardlinks, so unchanged files are copied and counted as such.
func TestWebDAVDestinationFullCycle(t *testing.T) {
	srv := davfake.New("/dav", "", "")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	job, src, _ := newTestJob(t)
	job.Destination = Destination{Type: "webdav", URL: ts.URL + "/dav", Path: "pc1"}
	writeFile(t, src, "docs/a.txt", "alpha", base0)
	writeFile(t, src, "b.txt", "beta", base0)
	ctx := context.Background()
	clk := &fixedClock{t: base0}

	r1, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Copied != 2 {
		t.Fatalf("first run copied=%d", r1.Copied)
	}

	writeFile(t, src, "b.txt", "beta-2", base0.Add(time.Hour))
	r2, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Plan.Counts()[KindUnchanged] != 1 || r2.Plan.Counts()[KindUpdate] != 1 {
		t.Fatalf("plan = %v", r2.Plan.Counts())
	}
	if r2.Linked != 0 || r2.Copied != 2 {
		t.Fatalf("remote run linked=%d copied=%d, want 0/2 (full copy per version)", r2.Linked, r2.Copied)
	}

	// Old version still restorable after the change.
	old := t.TempDir()
	if _, err := Restore(ctx, job, RestoreOptions{Snapshot: r1.Snapshot, Path: "b.txt"}, old); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(old, "b.txt")); got != "beta" {
		t.Fatalf("old version = %q", got)
	}
	out := t.TempDir()
	n, err := Restore(ctx, job, RestoreOptions{}, out)
	if err != nil || n != 2 {
		t.Fatalf("restore latest: n=%d err=%v", n, err)
	}
	if got := readFile(t, filepath.Join(out, "b.txt")); got != "beta-2" {
		t.Fatalf("latest = %q", got)
	}
}

// Encrypted job on S3 with the passphrase and nothing else supplied by the
// caller: the stored passphrase is used, and nothing readable reaches the bucket.
func TestEncryptedS3DestinationWithStoredPassphrase(t *testing.T) {
	s3 := remoteS3(t, "vault")
	job, src, _ := newTestJob(t)
	job.Encrypt = true
	job.Destination = Destination{Type: "s3", Bucket: "sync", Path: "vault"}
	secret := "quarterly numbers 42"
	writeFile(t, src, "finance/q3.txt", secret, base0)

	store := secrets.NewMemory()
	if err := store.Set(secrets.PassphraseAccount(job.Name), "bucket-pass-1"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	opts := Options{Hasher: &shaHasher{}, Now: (&fixedClock{t: base0}).now, Backend: s3, Secrets: store}
	res, err := Run(ctx, job, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Inspect every object the bucket received via the same backend: none may
	// contain the plaintext name or content.
	err = s3.Walk(ctx, "", func(fi backend.FileInfo) error {
		if strings.Contains(fi.Path, "finance") || strings.Contains(fi.Path, "q3") {
			t.Errorf("plaintext name in bucket: %s", fi.Path)
		}
		r, err := s3.Open(ctx, fi.Path)
		if err != nil {
			return err
		}
		defer r.Close()
		buf := make([]byte, 1<<16)
		n, _ := r.Read(buf)
		if strings.Contains(string(buf[:n]), secret) {
			t.Errorf("plaintext content in %s", fi.Path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	n, err := Restore(ctx, job, RestoreOptions{Snapshot: res.Snapshot, Backend: s3, Secrets: store}, out)
	if err != nil || n != 1 {
		t.Fatalf("restore: n=%d err=%v", n, err)
	}
	if got := readFile(t, filepath.Join(out, "finance", "q3.txt")); got != secret {
		t.Fatalf("restored %q", got)
	}

	// Wrong stored passphrase is refused on the next run.
	_ = store.Set(secrets.PassphraseAccount(job.Name), "wrong")
	if _, err := Run(ctx, job, opts); err == nil {
		t.Fatal("run with wrong stored passphrase should fail")
	}
}

// Missing destination password gives an actionable message, not a panic.
func TestMissingDestinationPasswordMessage(t *testing.T) {
	job, src, _ := newTestJob(t)
	job.Destination = Destination{Type: "webdav", URL: "https://dav.example.com/x", Username: "u"}
	writeFile(t, src, "a.txt", "x", base0)
	_, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}, Secrets: secrets.NewMemory()})
	if err == nil || !strings.Contains(err.Error(), "tpt-sync secret set") {
		t.Fatalf("err = %v", err)
	}
}
