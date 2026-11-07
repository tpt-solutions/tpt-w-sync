// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeebo/blake3"
)

// Benchmarks compare the Rust helper (subprocess) with an in-process pure-Go
// BLAKE3 baseline, and show what batching buys for many small files.
// Run with: TPT_SYNC_HASH=<path> go test -run '^$' -bench Hash ./engine

const benchFileSize = 64 << 20 // 64 MiB

func benchFile(b *testing.B, size int) string {
	b.Helper()
	p := filepath.Join(b.TempDir(), "big.bin")
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i * 131)
	}
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		b.Fatal(err)
	}
	return p
}

func requireHelper(b *testing.B) string {
	b.Helper()
	bin := os.Getenv(HashHelperEnv)
	if bin == "" {
		b.Skip("set " + HashHelperEnv + " to benchmark the Rust helper")
	}
	return bin
}

func BenchmarkHashLargeFileRustHelper(b *testing.B) {
	bin := requireHelper(b)
	p := benchFile(b, benchFileSize)
	b.SetBytes(benchFileSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := ExecHasher{Binary: bin}.Hash(context.Background(), []string{p})
		if err != nil || res[0].Err != nil {
			b.Fatal(err, res[0].Err)
		}
	}
}

func BenchmarkHashLargeFilePureGo(b *testing.B) {
	p := benchFile(b, benchFileSize)
	b.SetBytes(benchFileSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := os.Open(p)
		if err != nil {
			b.Fatal(err)
		}
		h := blake3.New()
		if _, err := io.Copy(h, f); err != nil {
			b.Fatal(err)
		}
		f.Close()
		_ = h.Sum(nil)
	}
}

// Many small files: one helper process for the whole set (batched) versus a
// process per file. Process start-up dominates the second case.
func benchSmallFiles(b *testing.B, n int) []string {
	b.Helper()
	dir := b.TempDir()
	paths := make([]string, n)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("f%05d.txt", i))
		if err := os.WriteFile(paths[i], []byte(paths[i]), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return paths
}

func BenchmarkHashManySmallFilesBatched(b *testing.B) {
	bin := requireHelper(b)
	paths := benchSmallFiles(b, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := (ExecHasher{Binary: bin}).Hash(context.Background(), paths); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(paths)), "files/op")
}

func BenchmarkHashManySmallFilesProcessPerFile(b *testing.B) {
	bin := requireHelper(b)
	paths := benchSmallFiles(b, 200) // fewer: each file costs a process spawn
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range paths {
			if _, err := (ExecHasher{Binary: bin}).Hash(context.Background(), []string{p}); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ReportMetric(float64(len(paths)), "files/op")
}
