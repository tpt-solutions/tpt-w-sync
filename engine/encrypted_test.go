// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine/crypt"
)

func encJob(t *testing.T) (Job, string, string) {
	t.Helper()
	job, src, dst := newTestJob(t)
	job.Encrypt = true
	return job, src, dst
}

func TestEncryptedRoundTripAndNoPlaintextOnDisk(t *testing.T) {
	job, src, dst := encJob(t)
	secret := "top secret contents 12345"
	writeFile(t, src, "private/plan-q3.txt", secret, base0)
	ctx := context.Background()
	pass := []byte("correct horse battery")

	res, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: (&fixedClock{t: base0}).now, Passphrase: pass})
	if err != nil {
		t.Fatal(err)
	}

	// Walk the whole destination: no plaintext names or content may appear.
	err = filepath.Walk(dst, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.Contains(p, "private") || strings.Contains(p, "plan-q3") {
			t.Errorf("plaintext path leaked into destination name: %s", p)
		}
		b, _ := os.ReadFile(p)
		if bytes.Contains(b, []byte(secret)) {
			t.Errorf("plaintext content found in %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	n, err := Restore(ctx, job, RestoreOptions{Snapshot: res.Snapshot, Passphrase: pass}, out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("restored %d files, want 1", n)
	}
	if got := readFile(t, filepath.Join(out, "private", "plan-q3.txt")); got != secret {
		t.Fatalf("restored content = %q", got)
	}
}

func TestEncryptedUnchangedFilesHardlinkAcrossSnapshots(t *testing.T) {
	job, src, dst := encJob(t)
	writeFile(t, src, "same.txt", "stable", base0)
	writeFile(t, src, "edit.txt", "v1", base0)
	ctx := context.Background()
	pass := []byte("pw")
	clk := &fixedClock{t: base0}
	r1, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now, Passphrase: pass})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, src, "edit.txt", "v2-longer", base0.Add(time.Hour))
	r2, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now, Passphrase: pass})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Linked != 1 || r2.Copied != 1 {
		t.Fatalf("linked=%d copied=%d, want 1/1", r2.Linked, r2.Copied)
	}
	k, _ := crypt.Derive(pass, mustParams(t, dst))
	name := k.ObfName("same.txt")
	a, _ := os.Stat(filepath.Join(dst, ".tpt-sync", "versions", r1.Snapshot, name))
	b, _ := os.Stat(filepath.Join(dst, ".tpt-sync", "versions", r2.Snapshot, name))
	if a == nil || b == nil || !os.SameFile(a, b) {
		t.Fatal("unchanged encrypted file not hardlinked across snapshots")
	}
}

func TestEncryptedWrongPassphraseRejected(t *testing.T) {
	job, src, _ := encJob(t)
	writeFile(t, src, "a.txt", "x", base0)
	ctx := context.Background()
	if _, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Passphrase: []byte("right")}); err != nil {
		t.Fatal(err)
	}
	_, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Passphrase: []byte("wrong")})
	if !errors.Is(err, crypt.ErrWrongPassphrase) {
		t.Fatalf("err = %v, want ErrWrongPassphrase", err)
	}
}

func TestEncryptedJobRequiresPassphrase(t *testing.T) {
	job, src, _ := encJob(t)
	writeFile(t, src, "a.txt", "x", base0)
	if _, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}}); err == nil {
		t.Fatal("encrypted run without passphrase should fail")
	}
}

func TestPlainJobRefusesEncryptedDestination(t *testing.T) {
	job, src, dst := encJob(t)
	writeFile(t, src, "a.txt", "x", base0)
	if _, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}, Passphrase: []byte("pw")}); err != nil {
		t.Fatal(err)
	}
	plain := job
	plain.Encrypt = false
	plain.Destination.Path = dst
	if _, err := Run(context.Background(), plain, Options{Hasher: &shaHasher{}}); err == nil {
		t.Fatal("plaintext run onto encrypted destination should be refused")
	}
}

func TestEncryptedDryRunWritesNothing(t *testing.T) {
	job, src, dst := encJob(t)
	writeFile(t, src, "a.txt", "x", base0)
	res, err := Run(context.Background(), job, Options{Hasher: &shaHasher{}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Counts()[KindCreate] != 1 {
		t.Fatalf("plan = %v", res.Plan.Counts())
	}
	if _, err := os.Stat(filepath.Join(dst, ".tpt-sync")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote key metadata")
	}
}

// mustParams reads the stored Argon2 parameters for a destination.
func mustParams(t *testing.T, dst string) crypt.Params {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dst, ".tpt-sync", "crypto.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m cryptoMeta
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m.Params
}
