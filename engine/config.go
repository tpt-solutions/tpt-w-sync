// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Store persists job definitions (jobs.json) and run state (state.json) in a
// config directory. Writes are atomic (temp file + rename).
type Store struct {
	Dir string
}

// DefaultConfigDir returns <user config dir>/tpt-sync.
func DefaultConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tpt-sync"), nil
}

// RunRecord is the outcome of the most recent run of a job.
type RunRecord struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Snapshot string    `json:"snapshot,omitempty"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Copied   int       `json:"copied"`
	Linked   int       `json:"linked"`
	Deleted  int       `json:"deleted"`
}

type stateFile struct {
	LastRun map[string]RunRecord `json:"last_run"`
}

// LoadJobs returns all jobs sorted by name. A missing file yields no jobs.
func (s Store) LoadJobs() ([]Job, error) {
	var jobs []Job
	err := readJSON(filepath.Join(s.Dir, "jobs.json"), &jobs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Name < jobs[j].Name })
	return jobs, nil
}

// SaveJobs replaces the stored job list after validating every job.
func (s Store) SaveJobs(jobs []Job) error {
	seen := map[string]bool{}
	for _, j := range jobs {
		if err := j.Validate(); err != nil {
			return fmt.Errorf("job %q: %w", j.Name, err)
		}
		if seen[j.Name] {
			return fmt.Errorf("duplicate job name %q", j.Name)
		}
		seen[j.Name] = true
	}
	return writeJSON(filepath.Join(s.Dir, "jobs.json"), jobs)
}

// Job returns the named job.
func (s Store) Job(name string) (Job, error) {
	jobs, err := s.LoadJobs()
	if err != nil {
		return Job{}, err
	}
	for _, j := range jobs {
		if j.Name == name {
			return j, nil
		}
	}
	return Job{}, fmt.Errorf("no such job %q", name)
}

// LastRuns returns the recorded last run per job name.
func (s Store) LastRuns() (map[string]RunRecord, error) {
	var st stateFile
	err := readJSON(filepath.Join(s.Dir, "state.json"), &st)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]RunRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	if st.LastRun == nil {
		st.LastRun = map[string]RunRecord{}
	}
	return st.LastRun, nil
}

// RecordRun stores the outcome of a job run.
func (s Store) RecordRun(name string, rec RunRecord) error {
	runs, err := s.LastRuns()
	if err != nil {
		return err
	}
	runs[name] = rec
	return writeJSON(filepath.Join(s.Dir, "state.json"), stateFile{LastRun: runs})
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
