// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestThrottleLimitsThroughput(t *testing.T) {
	const limit = 256 << 10 // 256 KiB/s
	data := make([]byte, 512<<10)
	lim := newLimiter(limit)
	start := time.Now()
	got, err := io.ReadAll(throttle(context.Background(), bytes.NewReader(data), lim))
	elapsed := time.Since(start)
	if err != nil || len(got) != len(data) {
		t.Fatalf("read %d bytes, err %v", len(got), err)
	}
	// 512 KiB at 256 KiB/s with a 64 KiB head start needs about 1.75 s.
	if elapsed < time.Second {
		t.Fatalf("transfer took %v; limit not applied", elapsed)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("transfer took %v; limiter too slow", elapsed)
	}
}

func TestThrottleCancellation(t *testing.T) {
	lim := newLimiter(1 << 10) // 1 KiB/s
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := io.ReadAll(throttle(ctx, bytes.NewReader(make([]byte, 1<<20)), lim))
	if err == nil {
		t.Fatal("cancelled throttled read returned no error")
	}
}

func TestNoLimitIsPassThrough(t *testing.T) {
	r := bytes.NewReader([]byte("abc"))
	if throttle(context.Background(), r, newLimiter(0)) != io.Reader(r) {
		t.Fatal("zero limit should not wrap the reader")
	}
}

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{
		"":      0,
		"1500":  1500,
		"64K":   64 << 10,
		"10mb":  10 << 20,
		"2G":    2 << 30,
		" 5 M ": 5 << 20,
	}
	for in, want := range cases {
		got, err := ParseBytes(in)
		if err != nil || got != want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"-1", "12X", "M", "1.5M"} {
		if _, err := ParseBytes(bad); err == nil {
			t.Errorf("ParseBytes(%q) should fail", bad)
		}
	}
}

// Two readers sharing one limiter draw from the same budget, so together they
// take about as long as one reader moving the combined bytes.
func TestSharedLimiterSpansReaders(t *testing.T) {
	const limit = 256 << 10
	shared := NewSharedLimiter(limit)
	start := time.Now()
	for i := 0; i < 2; i++ {
		data := make([]byte, 256<<10)
		if _, err := io.ReadAll(throttle(context.Background(), bytes.NewReader(data), shared)); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("shared budget not enforced across readers: %v", elapsed)
	}
	if NewSharedLimiter(0) != nil {
		t.Fatal("zero shared limit should be nil")
	}
}
