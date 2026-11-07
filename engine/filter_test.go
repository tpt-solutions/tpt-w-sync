// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import "testing"

func TestFilterMatching(t *testing.T) {
	cases := []struct {
		name    string
		f       Filters
		path    string
		allowed bool
	}{
		{"no rules allows all", Filters{}, "a/b.txt", true},
		{"basename glob matches any depth", Filters{Exclude: []string{"*.tmp"}}, "x/y/z.tmp", false},
		{"basename glob keeps others", Filters{Exclude: []string{"*.tmp"}}, "x/y/z.txt", true},
		{"double star excludes subtree", Filters{Exclude: []string{"cache/**"}}, "cache/a/b/c", false},
		{"double star matches zero segments", Filters{Exclude: []string{"cache/**"}}, "cache", false},
		{"double star does not leak to siblings", Filters{Exclude: []string{"cache/**"}}, "cached/x", true},
		{"include restricts", Filters{Include: []string{"docs/**"}}, "docs/a.md", true},
		{"include restricts others", Filters{Include: []string{"docs/**"}}, "src/a.go", false},
		{"exclude wins over include", Filters{Include: []string{"**/*.go"}, Exclude: []string{"vendor/**"}}, "vendor/x.go", false},
		{"path pattern anchored", Filters{Exclude: []string{"build/out.bin"}}, "build/out.bin", false},
		{"path pattern not at other depth", Filters{Exclude: []string{"build/out.bin"}}, "x/build/out.bin", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.Match(c.path); got != c.allowed {
				t.Fatalf("Match(%q) = %v, want %v", c.path, got, c.allowed)
			}
		})
	}
}

func TestFilterValidate(t *testing.T) {
	if err := (Filters{Exclude: []string{"["}}).Validate(); err == nil {
		t.Fatal("malformed glob should be rejected")
	}
	if err := (Filters{Include: []string{""}}).Validate(); err == nil {
		t.Fatal("empty pattern should be rejected")
	}
	if err := (Filters{Include: []string{"a/**/*.go"}}).Validate(); err != nil {
		t.Fatalf("valid pattern rejected: %v", err)
	}
}
