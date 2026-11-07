// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Local is a Backend rooted at a directory on the local filesystem.
type Local struct {
	root      string
	probeOnce sync.Once
	caps      Capabilities
}

// NewLocal opens a local backend at root. The directory is created if missing.
func NewLocal(root string) (*Local, error) {
	if root == "" {
		return nil, errors.New("backend: empty local root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("backend: creating root: %w", err)
	}
	return &Local{root: abs}, nil
}

// Root returns the absolute root directory of the backend.
func (l *Local) Root() string { return l.root }

// Capabilities reports the backend's optional features. Hardlink support is
// probed lazily, on first use, so backends that never need it (such as a
// source tree) do not create probe files in the tree they are reading.
func (l *Local) Capabilities() Capabilities {
	l.probeOnce.Do(func() { l.caps.Hardlink = probeHardlink(l.root) })
	return l.caps
}

// abs maps a slash-relative path to an absolute path under root, rejecting
// anything that would escape the root.
func (l *Local) abs(rel string) (string, error) {
	if rel == "" {
		return l.root, nil
	}
	if strings.ContainsRune(rel, 0) || path.IsAbs(rel) || filepath.IsAbs(rel) ||
		filepath.VolumeName(rel) != "" {
		return "", ErrPathEscape
	}
	clean := path.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrPathEscape
	}
	p := filepath.Join(l.root, filepath.FromSlash(clean))
	// Defense in depth: confirm the joined path is still inside root.
	if r, err := filepath.Rel(l.root, p); err != nil || r == ".." ||
		strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	return p, nil
}

// Walk visits regular files under dir. Symlinks and other non-regular entries
// are skipped so the engine never follows links out of the tree.
func (l *Local) Walk(ctx context.Context, dir string, fn func(FileInfo) error) error {
	base, err := l.abs(dir)
	if err != nil {
		return err
	}
	err = filepath.WalkDir(base, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(FileInfo{
			Path:    filepath.ToSlash(rel),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Stat returns information about one regular file.
func (l *Local) Stat(ctx context.Context, rel string) (FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return FileInfo{}, err
	}
	p, err := l.abs(rel)
	if err != nil {
		return FileInfo{}, err
	}
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return FileInfo{}, ErrNotExist
	}
	if err != nil {
		return FileInfo{}, err
	}
	if !info.Mode().IsRegular() {
		return FileInfo{}, ErrNotExist
	}
	return FileInfo{Path: path.Clean(rel), Size: info.Size(), ModTime: info.ModTime()}, nil
}

// Open opens a file for reading.
func (l *Local) Open(ctx context.Context, rel string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := l.abs(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotExist
	}
	return f, err
}

// Write atomically places r's contents at rel: data goes to a temporary file
// in the same directory, is synced, timestamped, then renamed over the target.
func (l *Local) Write(ctx context.Context, rel string, r io.Reader, modTime time.Time) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := l.abs(rel)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tpt-sync-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = io.Copy(tmp, contextReader{ctx: ctx, r: r}); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chtimes(tmpName, modTime, modTime); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

// Link creates newRel as a hardlink to existing.
func (l *Local) Link(ctx context.Context, existing, newRel string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !l.Capabilities().Hardlink {
		return ErrUnsupported
	}
	src, err := l.abs(existing)
	if err != nil {
		return err
	}
	dst, err := l.abs(newRel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err = os.Link(src, dst)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotExist
	}
	return err
}

// Delete removes one file.
func (l *Local) Delete(ctx context.Context, rel string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := l.abs(rel)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotExist
	}
	return err
}

// ListDirs returns the sorted names of immediate subdirectories of dir.
func (l *Local) ListDirs(ctx context.Context, dir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := l.abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// RemoveTree recursively deletes a directory under the root.
func (l *Local) RemoveTree(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := l.abs(dir)
	if err != nil {
		return err
	}
	if p == l.root {
		return ErrPathEscape // never remove the whole backend
	}
	return os.RemoveAll(p)
}

// probeHardlink reports whether the filesystem at dir supports hardlinks.
func probeHardlink(dir string) bool {
	f, err := os.CreateTemp(dir, ".tpt-sync-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	defer os.Remove(name)
	link := name + ".link"
	if err := os.Link(name, link); err != nil {
		return false
	}
	_ = os.Remove(link)
	return true
}

// contextReader stops a copy as soon as ctx is cancelled.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
