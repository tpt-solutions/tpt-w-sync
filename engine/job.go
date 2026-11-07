// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine/schedule"
)

// Destination identifies where a job writes its snapshots.
//
// Type selects the backend:
//   - "local":  Path is a directory.
//   - "webdav": URL is the server endpoint; Path is a folder within it;
//     Username enables basic auth. The password is kept in the OS keychain.
//   - "s3":     Bucket (and optional Prefix folder) on an S3-compatible
//     endpoint (URL; empty means AWS). Username is the access key ID; the
//     secret key is kept in the OS keychain.
type Destination struct {
	Type     string `json:"type"`
	Path     string `json:"path,omitempty"`
	URL      string `json:"url,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	Region   string `json:"region,omitempty"`
	Username string `json:"username,omitempty"`
}

// Versioning controls snapshot retention.
type Versioning struct {
	// MaxVersions keeps at most this many snapshots (0 = unlimited).
	MaxVersions int `json:"max_versions,omitempty"`
	// MaxAge removes snapshots older than this (0 = unlimited).
	MaxAge time.Duration `json:"max_age,omitempty"`
	// Store selects where snapshots live: "" (the default, the destination
	// layout) or "archon" (Archon-native snapshots, Phase 11). The archon store
	// is not available in this build, so jobs that select it are rejected.
	Store string `json:"store,omitempty"`
}

// Job is one source → destination sync definition.
type Job struct {
	Name        string      `json:"name"`
	Source      string      `json:"source"`
	Destination Destination `json:"destination"`
	Filters     Filters     `json:"filters,omitempty"`
	Versioning  Versioning  `json:"versioning,omitempty"`
	// Concurrency bounds parallel file transfers (0 = DefaultConcurrency).
	Concurrency int `json:"concurrency,omitempty"`
	// Schedule is a cron expression (see package schedule); empty = none.
	Schedule string `json:"schedule,omitempty"`
	// Watch enables real-time filesystem-triggered runs.
	Watch bool `json:"watch,omitempty"`
	// Enabled controls whether the scheduler runs this job automatically.
	// Manual runs are always allowed.
	Enabled bool `json:"enabled"`
	// Encrypt enables end-to-end encryption of the destination. Requires a
	// passphrase at run time; the destination must be dedicated to this job.
	Encrypt bool `json:"encrypt,omitempty"`
	// BandwidthLimit caps upload throughput for the whole job, in bytes per
	// second (0 = unlimited).
	BandwidthLimit int64 `json:"bandwidth_limit,omitempty"`
	// MissedRun decides what happens to a scheduled run that was missed while
	// the daemon was not running: "catch-up" (default) runs once at startup,
	// "skip" waits for the next scheduled tick.
	MissedRun string `json:"missed_run,omitempty"`
}

// DefaultConcurrency is used when a job does not set Concurrency.
const DefaultConcurrency = 4

var jobNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Validate checks a job definition for errors that would make it unrunnable.
func (j Job) Validate() error {
	if !jobNameRE.MatchString(j.Name) {
		return fmt.Errorf("invalid job name %q (letters, digits, . _ - ; max 64)", j.Name)
	}
	if j.Source == "" {
		return errors.New("source is required")
	}
	switch j.Destination.Type {
	case "local":
		if j.Destination.Path == "" {
			return errors.New("destination path is required")
		}
	case "webdav":
		if j.Destination.URL == "" {
			return errors.New("webdav destination needs a url")
		}
	case "s3":
		if j.Destination.Bucket == "" {
			return errors.New("s3 destination needs a bucket")
		}
	case "":
		return errors.New("destination type is required")
	default:
		return fmt.Errorf("unsupported destination type %q", j.Destination.Type)
	}
	if err := j.Filters.Validate(); err != nil {
		return err
	}
	if j.Versioning.MaxVersions < 0 || j.Versioning.MaxAge < 0 {
		return errors.New("versioning limits must be non-negative")
	}
	switch j.Versioning.Store {
	case "", "destination":
	case "archon":
		return errors.New("versioning store \"archon\" is not available in this build (see docs/archon-design.md)")
	default:
		return fmt.Errorf("unknown versioning store %q", j.Versioning.Store)
	}
	if j.Concurrency < 0 {
		return errors.New("concurrency must be non-negative")
	}
	if j.BandwidthLimit < 0 {
		return errors.New("bandwidth limit must be non-negative")
	}
	switch j.MissedRun {
	case "", "catch-up", "skip":
	default:
		return fmt.Errorf("missed_run must be \"catch-up\" or \"skip\", not %q", j.MissedRun)
	}
	if j.Schedule != "" {
		if _, err := schedule.Parse(j.Schedule); err != nil {
			return fmt.Errorf("schedule: %w", err)
		}
	}
	return nil
}

func (j Job) concurrency() int {
	if j.Concurrency <= 0 {
		return DefaultConcurrency
	}
	return j.Concurrency
}
