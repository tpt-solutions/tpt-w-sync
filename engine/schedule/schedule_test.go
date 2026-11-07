// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package schedule

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func mustParse(t *testing.T, spec string) *Schedule {
	t.Helper()
	s, err := Parse(spec)
	if err != nil {
		t.Fatalf("Parse(%q): %v", spec, err)
	}
	return s
}

func TestCronNext(t *testing.T) {
	loc := time.Local
	at := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, loc)
	}
	cases := []struct {
		spec string
		from time.Time
		want time.Time
	}{
		{"0 2 * * *", at(2026, 3, 10, 1, 30), at(2026, 3, 10, 2, 0)},
		{"0 2 * * *", at(2026, 3, 10, 2, 0), at(2026, 3, 11, 2, 0)}, // strictly after
		{"*/15 * * * *", at(2026, 3, 10, 10, 7), at(2026, 3, 10, 10, 15)},
		{"30 8 * * 1-5", at(2026, 3, 13, 9, 0), at(2026, 3, 16, 8, 30)}, // Fri 13 → Mon 16
		{"0 0 1 * *", at(2026, 3, 2, 0, 0), at(2026, 4, 1, 0, 0)},
		{"0 12 29 2 *", at(2026, 1, 1, 0, 0), at(2028, 2, 29, 12, 0)}, // leap day
		{"@daily", at(2026, 3, 10, 5, 0), at(2026, 3, 11, 0, 0)},
		{"0 9 * * 0", at(2026, 3, 10, 0, 0), at(2026, 3, 15, 9, 0)}, // Tue → Sun
		{"0 9 * * 7", at(2026, 3, 10, 0, 0), at(2026, 3, 15, 9, 0)}, // 7 == Sunday
	}
	for _, c := range cases {
		got := mustParse(t, c.spec).Next(c.from)
		if !got.Equal(c.want) {
			t.Errorf("%q from %v: got %v, want %v", c.spec, c.from, got, c.want)
		}
	}
}

func TestCronDomOrDowWhenBothRestricted(t *testing.T) {
	// "0 0 13 * 5": 13th of the month OR any Friday.
	s := mustParse(t, "0 0 13 * 5")
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local) // Sunday
	got := s.Next(from)
	want := time.Date(2026, 3, 6, 0, 0, 0, 0, time.Local) // first Friday
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCronParseErrors(t *testing.T) {
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *", "1-x * * * *", "@sometimes"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestDebouncerCoalesces(t *testing.T) {
	var fired atomic.Int32
	d := NewDebouncer(80*time.Millisecond, 0, func() { fired.Add(1) })
	for i := 0; i < 10; i++ {
		d.Trigger()
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if n := fired.Load(); n != 1 {
		t.Fatalf("fired %d times, want 1", n)
	}
}

func TestDebouncerMaxWaitForcesFire(t *testing.T) {
	var fired atomic.Int32
	d := NewDebouncer(100*time.Millisecond, 250*time.Millisecond, func() { fired.Add(1) })
	stop := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(stop) {
		d.Trigger()
		time.Sleep(30 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Fatal("maxWait did not force a fire during continuous churn")
	}
	d.Stop()
}

func TestDebouncerStop(t *testing.T) {
	var fired atomic.Int32
	d := NewDebouncer(50*time.Millisecond, 0, func() { fired.Add(1) })
	d.Trigger()
	d.Stop()
	time.Sleep(120 * time.Millisecond)
	if fired.Load() != 0 {
		t.Fatal("Stop did not cancel pending fire")
	}
}

func TestWatchTreeReportsChanges(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan struct{}, 16)
	go func() { _ = WatchTree(ctx, root, func() { changed <- struct{}{} }) }()

	time.Sleep(200 * time.Millisecond) // let the watcher register
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no change event delivered")
	}
}
