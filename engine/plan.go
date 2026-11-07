// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine/backend"
)

// Kind is the planned action for one path.
type Kind string

const (
	// KindCreate: file is new relative to the previous snapshot; copy it.
	KindCreate Kind = "create"
	// KindUpdate: file changed; copy the new content.
	KindUpdate Kind = "update"
	// KindUnchanged: content identical; hardlink from the previous snapshot.
	KindUnchanged Kind = "unchanged"
	// KindDelete: file no longer in the source; omitted from the new snapshot
	// (it remains available in older snapshots).
	KindDelete Kind = "delete"
)

// mtimeTolerance treats modification times within this window as equal, to
// allow for coarse timestamps on FAT/exFAT-style filesystems (2 s resolution).
const mtimeTolerance = 2 * time.Second

// Action is one planned change.
type Action struct {
	Path    string
	Kind    Kind
	Size    int64
	ModTime time.Time
	// Reason explains why the action was chosen (useful for dry-run output).
	Reason string
}

// Plan is the ordered set of actions that produce a new snapshot.
type Plan struct {
	Actions []Action
}

// Counts returns the number of actions of each kind.
func (p Plan) Counts() map[Kind]int {
	c := map[Kind]int{}
	for _, a := range p.Actions {
		c[a.Kind]++
	}
	return c
}

// BuildPlan compares the current source tree against the previous snapshot.
//
// Decision order per path, cheapest first:
//  1. absent from previous snapshot → create
//  2. size differs → update
//  3. size and mtime equal → unchanged
//  4. size equal, mtime differs → checksum both; equal → unchanged, else update
//
// Paths present only in the previous snapshot become delete actions. If the
// checksum of either side cannot be computed, the file is conservatively
// treated as updated. srcRoot and prevRoot are local directories.
func BuildPlan(ctx context.Context, src, prev map[string]backend.FileInfo,
	srcRoot, prevRoot string, hasher Hasher) (Plan, error) {

	var plan Plan
	var ambiguous []string
	ambiguousIdx := map[string]int{}

	paths := make([]string, 0, len(src))
	for p := range src {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		s := src[p]
		old, ok := prev[p]
		switch {
		case !ok:
			plan.Actions = append(plan.Actions, Action{Path: p, Kind: KindCreate,
				Size: s.Size, ModTime: s.ModTime, Reason: "new file"})
		case s.Size != old.Size:
			plan.Actions = append(plan.Actions, Action{Path: p, Kind: KindUpdate,
				Size: s.Size, ModTime: s.ModTime, Reason: "size changed"})
		case sameTime(s.ModTime, old.ModTime):
			plan.Actions = append(plan.Actions, Action{Path: p, Kind: KindUnchanged,
				Size: s.Size, ModTime: s.ModTime, Reason: "size and mtime unchanged"})
		default:
			ambiguousIdx[p] = len(plan.Actions)
			ambiguous = append(ambiguous, p)
			plan.Actions = append(plan.Actions, Action{Path: p, Kind: KindUnchanged,
				Size: s.Size, ModTime: s.ModTime})
		}
	}

	if len(ambiguous) > 0 {
		if err := resolveByChecksum(ctx, ambiguous, ambiguousIdx, &plan,
			srcRoot, prevRoot, hasher); err != nil {
			return Plan{}, err
		}
	}

	for _, p := range sortedKeys(prev) {
		if _, ok := src[p]; !ok {
			old := prev[p]
			plan.Actions = append(plan.Actions, Action{Path: p, Kind: KindDelete,
				Size: old.Size, ModTime: old.ModTime, Reason: "removed from source"})
		}
	}
	return plan, nil
}

func resolveByChecksum(ctx context.Context, paths []string, idx map[string]int,
	plan *Plan, srcRoot, prevRoot string, hasher Hasher) error {

	if prevRoot == "" {
		// Previous content is not readable as plaintext (encrypted snapshot),
		// so it cannot be compared; copy conservatively.
		for _, p := range paths {
			a := &plan.Actions[idx[p]]
			a.Kind = KindUpdate
			a.Reason = "mtime changed; previous content not verifiable (encrypted)"
		}
		return nil
	}
	srcAbs := make([]string, len(paths))
	prevAbs := make([]string, len(paths))
	for i, p := range paths {
		srcAbs[i] = filepath.Join(srcRoot, filepath.FromSlash(p))
		prevAbs[i] = filepath.Join(prevRoot, filepath.FromSlash(p))
	}
	srcSums, err := hasher.Hash(ctx, srcAbs)
	if err != nil {
		return err
	}
	prevSums, err := hasher.Hash(ctx, prevAbs)
	if err != nil {
		return err
	}
	for i, p := range paths {
		a := &plan.Actions[idx[p]]
		switch {
		case srcSums[i].Err != nil:
			a.Kind = KindUpdate
			a.Reason = "checksum failed on source: " + srcSums[i].Err.Error()
		case prevSums[i].Err != nil:
			a.Kind = KindUpdate
			a.Reason = "checksum failed on previous snapshot"
		case srcSums[i].Sum == prevSums[i].Sum:
			a.Kind = KindUnchanged
			a.Reason = "checksum unchanged (mtime differs)"
		default:
			a.Kind = KindUpdate
			a.Reason = "checksum changed"
		}
	}
	return nil
}

func sameTime(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= mtimeTolerance
}

func sortedKeys(m map[string]backend.FileInfo) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
