// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package backend

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestLocalRejectsPathEscape(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, bad := range []string{"../outside", "a/../../outside", "/etc/passwd", "..", "C:/x"} {
		if _, err := l.Stat(ctx, bad); !errors.Is(err, ErrPathEscape) {
			t.Errorf("Stat(%q) error = %v, want ErrPathEscape", bad, err)
		}
		if err := l.Write(ctx, bad, strings.NewReader("x"), time.Now()); !errors.Is(err, ErrPathEscape) {
			t.Errorf("Write(%q) error = %v, want ErrPathEscape", bad, err)
		}
	}
	if err := l.RemoveTree(ctx, ""); !errors.Is(err, ErrPathEscape) {
		t.Errorf("RemoveTree of root must be refused, got %v", err)
	}
}

func TestLocalWriteIsAtomicAndKeepsHardlinks(t *testing.T) {
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !l.Capabilities().Hardlink {
		t.Skip("filesystem does not support hardlinks")
	}
	ctx := context.Background()
	mt := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := l.Write(ctx, "dir/f.txt", strings.NewReader("one"), mt); err != nil {
		t.Fatal(err)
	}
	if err := l.Link(ctx, "dir/f.txt", "copy/f.txt"); err != nil {
		t.Fatal(err)
	}
	// Overwriting one name must not change the other (no in-place writes).
	if err := l.Write(ctx, "dir/f.txt", strings.NewReader("two"), mt); err != nil {
		t.Fatal(err)
	}
	r, err := l.Open(ctx, "copy/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	if string(b) != "one" {
		t.Fatalf("hardlinked copy changed to %q", b)
	}
	fi, err := l.Stat(ctx, "dir/f.txt")
	if err != nil || !fi.ModTime.Equal(mt) {
		t.Fatalf("mtime not preserved: %v %v", fi.ModTime, err)
	}
}

func TestLocalMissingFiles(t *testing.T) {
	l, _ := NewLocal(t.TempDir())
	ctx := context.Background()
	if _, err := l.Stat(ctx, "nope"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Stat missing = %v", err)
	}
	if err := l.Walk(ctx, "nodir", func(FileInfo) error { return nil }); err != nil {
		t.Fatalf("Walk of missing dir should be empty, got %v", err)
	}
	dirs, err := l.ListDirs(ctx, "nodir")
	if err != nil || len(dirs) != 0 {
		t.Fatalf("ListDirs missing = %v, %v", dirs, err)
	}
}
