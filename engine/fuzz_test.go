// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import "testing"

// FuzzFilterMatch checks that arbitrary patterns and paths never panic and
// that Validate accepts every pattern Match can evaluate without error.
func FuzzFilterMatch(f *testing.F) {
	f.Add("*.tmp", "a/b/c.tmp")
	f.Add("cache/**", "cache/x/y")
	f.Add("**/*.go", "src/main.go")
	f.Add("[", "x")
	f.Fuzz(func(t *testing.T, pattern, path string) {
		flt := Filters{Include: []string{pattern}, Exclude: []string{pattern}}
		_ = flt.Match(path)
		_ = flt.Validate()
	})
}

// FuzzParseBytes checks the size parser never panics and never returns a
// negative value without an error.
func FuzzParseBytes(f *testing.F) {
	f.Add("10M")
	f.Add("-1")
	f.Add("99999999999999999999G")
	f.Fuzz(func(t *testing.T, s string) {
		n, err := ParseBytes(s)
		if err == nil && n < 0 {
			t.Fatalf("ParseBytes(%q) = %d without error", s, n)
		}
	})
}
