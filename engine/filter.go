// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"fmt"
	"path"
	"strings"
)

// Filters are include/exclude glob patterns applied to slash-relative paths.
//
// Pattern syntax: path.Match globs per segment, plus "**" which matches zero
// or more whole path segments. A pattern without "/" matches the basename at
// any depth (so "*.tmp" excludes every .tmp file). Exclude wins over include.
// If Include is empty, every non-excluded path is included.
type Filters struct {
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

// Validate checks that every pattern is well-formed.
func (f Filters) Validate() error {
	for _, p := range append(append([]string{}, f.Include...), f.Exclude...) {
		if p == "" {
			return fmt.Errorf("empty filter pattern")
		}
		if _, err := path.Match(segmentOf(p), ""); err != nil {
			return fmt.Errorf("bad filter pattern %q: %w", p, err)
		}
	}
	return nil
}

// Match reports whether rel (slash-separated) passes the filters.
func (f Filters) Match(rel string) bool {
	for _, p := range f.Exclude {
		if matchPattern(p, rel) {
			return false
		}
	}
	if len(f.Include) == 0 {
		return true
	}
	for _, p := range f.Include {
		if matchPattern(p, rel) {
			return true
		}
	}
	return false
}

func segmentOf(p string) string {
	// Only used to validate syntax of a single segment; "**" is always valid.
	if p == "**" {
		return "x"
	}
	return p
}

func matchPattern(pattern, rel string) bool {
	if !strings.Contains(pattern, "/") {
		return matchSegments([]string{pattern}, []string{path.Base(rel)}) ||
			matchSegments(strings.Split(pattern, "/"), strings.Split(rel, "/"))
	}
	return matchSegments(strings.Split(strings.TrimPrefix(pattern, "/"), "/"),
		strings.Split(rel, "/"))
}

// matchSegments matches pattern segments against path segments, treating a
// "**" segment as matching zero or more path segments.
func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}
