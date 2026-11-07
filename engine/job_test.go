// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"strings"
	"testing"
)

func validJob() Job {
	return Job{
		Name:        "docs",
		Source:      "/data",
		Destination: Destination{Type: "local", Path: "/backup"},
	}
}

func TestJobValidation(t *testing.T) {
	if err := validJob().Validate(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	cases := map[string]func(*Job){
		"bad name":       func(j *Job) { j.Name = "bad name!" },
		"no source":      func(j *Job) { j.Source = "" },
		"no dest type":   func(j *Job) { j.Destination.Type = "" },
		"unknown type":   func(j *Job) { j.Destination.Type = "ftp" },
		"webdav no url":  func(j *Job) { j.Destination = Destination{Type: "webdav"} },
		"s3 no bucket":   func(j *Job) { j.Destination = Destination{Type: "s3"} },
		"negative limit": func(j *Job) { j.BandwidthLimit = -1 },
		"bad schedule":   func(j *Job) { j.Schedule = "99 * * * *" },
		"bad filter":     func(j *Job) { j.Filters.Exclude = []string{"["} },
	}
	for name, mutate := range cases {
		j := validJob()
		mutate(&j)
		if err := j.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The Archon snapshot store is a stub: selecting it must fail validation with
// a message that points at the design, rather than silently falling back.
func TestArchonStoreIsRejectedInThisBuild(t *testing.T) {
	j := validJob()
	j.Versioning.Store = "archon"
	err := j.Validate()
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v", err)
	}
	j.Versioning.Store = "destination"
	if err := j.Validate(); err != nil {
		t.Fatalf("explicit destination store rejected: %v", err)
	}
	j.Versioning.Store = "tape"
	if err := j.Validate(); err == nil {
		t.Fatal("unknown store accepted")
	}
}
