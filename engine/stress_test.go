// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestStressLargeTree measures planning and no-change runs on a large tree.
// It is opt-in because it writes many files:
//
//	TPT_SYNC_STRESS=100000 go test -run TestStressLargeTree -v -timeout 30m ./engine
func TestStressLargeTree(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("TPT_SYNC_STRESS"))
	if err != nil || n <= 0 {
		t.Skip("set TPT_SYNC_STRESS=<file count> to run the large-tree stress test")
	}
	job, src, _ := newTestJob(t)
	ctx := context.Background()

	const dirs = 100
	for i := 0; i < n; i++ {
		dir := filepath.Join(src, fmt.Sprintf("d%03d", i%dirs))
		if i < dirs {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		p := filepath.Join(dir, fmt.Sprintf("f%07d.dat", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("file %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	clk := &fixedClock{t: base0}
	start := time.Now()
	first, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	firstDur := time.Since(start)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	start = time.Now()
	second, err := Run(ctx, job, Options{Hasher: &shaHasher{}, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	secondDur := time.Since(start)

	if second.Linked != n || second.Copied != 0 {
		t.Fatalf("no-change run linked=%d copied=%d, want %d/0", second.Linked, second.Copied, n)
	}
	t.Logf("files=%d", n)
	t.Logf("first run (copy everything): %v, copied=%d", firstDur, first.Copied)
	t.Logf("no-change run (hardlink everything): %v, linked=%d", secondDur, second.Linked)
	t.Logf("heap after first run: %.1f MiB", float64(mem.HeapAlloc)/(1<<20))
}
