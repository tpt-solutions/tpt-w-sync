// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integration test against the real Rust helper. Skipped unless
// TPT_SYNC_HASH points at a built tpt-sync-hash binary.
func TestExecHasherAgainstRustHelper(t *testing.T) {
	bin := os.Getenv(HashHelperEnv)
	if bin == "" {
		t.Skip("set " + HashHelperEnv + " to run the helper integration test")
	}
	dir := t.TempDir()
	abc := filepath.Join(dir, "abc.bin")
	if err := os.WriteFile(abc, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.bin")
	withNewline := filepath.Join(dir, "bad\nname")

	res, err := ExecHasher{Binary: bin}.Hash(context.Background(), []string{abc, missing, withNewline})
	if err != nil {
		t.Fatal(err)
	}
	const abcHash = "6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85"
	if res[0].Err != nil || res[0].Sum != abcHash {
		t.Fatalf("abc: sum=%q err=%v", res[0].Sum, res[0].Err)
	}
	if res[1].Err == nil {
		t.Fatal("missing file should report an error")
	}
	if res[2].Err == nil || !strings.Contains(res[2].Err.Error(), "newline") {
		t.Fatalf("newline path should be rejected, got %v", res[2].Err)
	}
}

// Batching: more paths than one batch must still return ordered results.
func TestExecHasherBatchesLargeInputs(t *testing.T) {
	bin := os.Getenv(HashHelperEnv)
	if bin == "" {
		t.Skip("set " + HashHelperEnv + " to run the helper integration test")
	}
	dir := t.TempDir()
	var paths []string
	for i := 0; i < hashBatchSize+3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%04d.bin", i))
		if err := os.WriteFile(p, []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	res, err := ExecHasher{Binary: bin}.Hash(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != len(paths) {
		t.Fatalf("got %d results for %d paths", len(res), len(paths))
	}
	for i, r := range res {
		if r.Path != paths[i] || r.Err != nil || len(r.Sum) != 64 {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
}
