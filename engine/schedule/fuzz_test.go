// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package schedule

import (
	"testing"
	"time"
)

// FuzzParse checks that the cron parser never panics, and that any schedule
// it accepts can compute a next run without panicking.
func FuzzParse(f *testing.F) {
	f.Add("0 2 * * *")
	f.Add("*/15 9-17 * * 1-5")
	f.Add("@daily")
	f.Add("0,30 * 29 2 7")
	f.Fuzz(func(t *testing.T, spec string) {
		s, err := Parse(spec)
		if err != nil {
			return
		}
		_ = s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	})
}
