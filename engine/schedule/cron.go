// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package schedule provides cron-style schedules, change debouncing, and
// recursive filesystem watching for the tpt-sync scheduler.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed five-field cron expression evaluated in local time:
//
//	minute hour day-of-month month day-of-week
//
// Supported syntax per field: "*", "n", "a-b", "*/n", "a-b/n", "a/n", and
// comma-separated lists of these. Day-of-week is 0-6 with 0 = Sunday (7 is
// accepted as Sunday too). If both day-of-month and day-of-week are restricted
// (not "*"), a time matches when either matches, as in classic cron.
// Aliases: @hourly, @daily (@midnight), @weekly, @monthly, @yearly (@annually).
type Schedule struct {
	minute, hour, dom, month, dow fieldSet
	domStar, dowStar              bool
}

type fieldSet struct {
	allowed [64]bool
}

func (f fieldSet) has(v int) bool { return v >= 0 && v < len(f.allowed) && f.allowed[v] }

var aliases = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

// Parse parses a cron expression.
func Parse(spec string) (*Schedule, error) {
	spec = strings.TrimSpace(spec)
	if a, ok := aliases[spec]; ok {
		spec = a
	}
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: want 5 fields, got %d in %q", len(fields), spec)
	}
	s := &Schedule{}
	var err error
	if s.minute, _, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if s.hour, _, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if s.dom, s.domStar, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	if s.month, _, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if s.dow, s.dowStar, err = parseField(fields[4], 0, 7); err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	// Fold Sunday=7 into Sunday=0.
	if s.dow.allowed[7] {
		s.dow.allowed[0] = true
	}
	return s, nil
}

func parseField(f string, lo, hi int) (fieldSet, bool, error) {
	var fs fieldSet
	star := f == "*"
	for _, part := range strings.Split(f, ",") {
		if part == "" {
			return fs, star, fmt.Errorf("empty list element in %q", f)
		}
		rangePart, stepPart, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepPart)
			if err != nil || n <= 0 {
				return fs, star, fmt.Errorf("bad step %q", stepPart)
			}
			step = n
		}
		var a, b int
		switch {
		case rangePart == "*":
			a, b = lo, hi
		case strings.Contains(rangePart, "-"):
			x, y, _ := strings.Cut(rangePart, "-")
			var err1, err2 error
			a, err1 = strconv.Atoi(x)
			b, err2 = strconv.Atoi(y)
			if err1 != nil || err2 != nil {
				return fs, star, fmt.Errorf("bad range %q", rangePart)
			}
		default:
			n, err := strconv.Atoi(rangePart)
			if err != nil {
				return fs, star, fmt.Errorf("bad value %q", rangePart)
			}
			a = n
			b = n
			if hasStep {
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return fs, star, fmt.Errorf("%q out of range %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			fs.allowed[v] = true
		}
	}
	return fs, star, nil
}

// Next returns the first matching time strictly after t, in t's location.
// It returns the zero time if no match exists within five years.
func (s *Schedule) Next(t time.Time) time.Time {
	limit := t.AddDate(5, 0, 0)
	cur := t.Truncate(time.Minute).Add(time.Minute)
	loc := cur.Location()
	for cur.Before(limit) {
		if !s.month.has(int(cur.Month())) {
			cur = time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(cur) {
			cur = time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.hour.has(cur.Hour()) {
			cur = time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour()+1, 0, 0, 0, loc)
			continue
		}
		if !s.minute.has(cur.Minute()) {
			cur = cur.Add(time.Minute)
			continue
		}
		return cur
	}
	return time.Time{}
}

func (s *Schedule) dayMatches(t time.Time) bool {
	domOK := s.dom.has(t.Day())
	dowOK := s.dow.has(int(t.Weekday()))
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dowOK
	case s.dowStar:
		return domOK
	default:
		return domOK || dowOK
	}
}
