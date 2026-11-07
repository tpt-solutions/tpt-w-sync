// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package backend defines the storage abstraction used by the sync engine for
// both sources and destinations. All paths are slash-separated and relative to
// the backend root.
package backend

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrUnsupported is returned when a backend lacks a capability, e.g. hardlinks.
var ErrUnsupported = errors.New("backend: operation not supported")

// ErrNotExist is returned when a path does not exist.
var ErrNotExist = errors.New("backend: path does not exist")

// ErrPathEscape is returned for paths that are absolute or climb above the root.
var ErrPathEscape = errors.New("backend: path escapes backend root")

// FileInfo describes a regular file within a backend.
type FileInfo struct {
	Path    string // slash-separated, relative to the backend root
	Size    int64
	ModTime time.Time
}

// Capabilities describes optional behaviour a backend supports.
type Capabilities struct {
	// Hardlink reports whether Link can create hardlinks, enabling
	// space-efficient snapshot dedup. When false, callers fall back to copying.
	Hardlink bool
}

// Backend is a storage location a sync job can read from or write to.
type Backend interface {
	// Capabilities reports optional features of this backend.
	Capabilities() Capabilities

	// Walk calls fn for every regular file under dir (relative to the root;
	// "" means the whole backend). A missing dir is not an error (no files).
	Walk(ctx context.Context, dir string, fn func(FileInfo) error) error

	// Stat returns information about one file. Returns ErrNotExist if absent.
	Stat(ctx context.Context, rel string) (FileInfo, error)

	// Open opens a file for reading. Returns ErrNotExist if absent.
	Open(ctx context.Context, rel string) (io.ReadCloser, error)

	// Write atomically creates or replaces rel with the contents of r, creating
	// parent directories as needed and setting the modification time. Existing
	// files are never written in place: a new file is created and renamed over
	// the old name, so hardlinks to the old content are unaffected.
	Write(ctx context.Context, rel string, r io.Reader, modTime time.Time) error

	// Link creates newRel as a hardlink to existing. Returns ErrUnsupported
	// when Capabilities().Hardlink is false.
	Link(ctx context.Context, existing, newRel string) error

	// Delete removes one file. Returns ErrNotExist if absent.
	Delete(ctx context.Context, rel string) error

	// ListDirs returns the names (not paths) of the immediate subdirectories of
	// dir, sorted ascending. A missing dir yields none. Unlike Walk, this sees
	// empty directories.
	ListDirs(ctx context.Context, dir string) ([]string, error)

	// RemoveTree recursively removes a directory. A missing dir is not an error.
	RemoveTree(ctx context.Context, dir string) error
}
