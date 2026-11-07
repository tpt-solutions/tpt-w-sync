// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package schedule

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// WatchTree watches root and all directories beneath it, calling onChange for
// each relevant filesystem event (create, write, remove, rename). New
// subdirectories are added to the watch automatically. Events for tpt-sync's
// own metadata directory are ignored. It returns when ctx is cancelled.
//
// On Linux this uses inotify; on Windows it uses ReadDirectoryChangesW (both
// via fsnotify).
func WatchTree(ctx context.Context, root string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than failing the watch
		}
		if d.IsDir() && !ignored(root, p) {
			return w.Add(p)
		}
		return nil
	})
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			_ = err // transient watch errors are non-fatal; keep watching
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ignored(root, ev.Name) || ev.Op == fsnotify.Chmod {
				continue
			}
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = filepath.WalkDir(ev.Name, func(p string, d fs.DirEntry, err error) error {
						if err == nil && d.IsDir() && !ignored(root, p) {
							_ = w.Add(p)
						}
						return nil
					})
				}
			}
			onChange()
		}
	}
}

func ignored(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return true
	}
	// Ignore tpt-sync's own metadata and temp/probe files (.tpt-sync, .tpt-sync-*)
	// so that writing them can never retrigger a sync.
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(seg, ".tpt-sync") {
			return true
		}
	}
	return false
}
