// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package daemon hosts the scheduler: it runs enabled jobs on their cron
// schedules and on filesystem change (watch) triggers, independent of any UI.
package daemon

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine"
	"github.com/tpt-solutions/tpt-sync/engine/schedule"
	"github.com/tpt-solutions/tpt-sync/engine/secrets"
)

// WatchDebounce is the quiet period before a watch-triggered run starts.
const WatchDebounce = 2 * time.Second

// WatchMaxWait forces a watch-triggered run even during continuous churn.
const WatchMaxWait = 30 * time.Second

// Daemon runs scheduled and watch-triggered jobs until its context ends.
type Daemon struct {
	Store  engine.Store
	Hasher engine.Hasher // nil = default Rust helper
	Log    io.Writer     // nil = discard
	Now    func() time.Time
	// Secrets supplies passphrases and remote destination passwords.
	Secrets secrets.Store

	// per-job state: a run lock, and a flag set when a trigger arrived mid-run.
	mu      sync.Mutex
	running map[string]bool
	pending map[string]bool
}

func (d *Daemon) logf(format string, args ...any) {
	if d.Log != nil {
		fmt.Fprintf(d.Log, time.Now().Format("2006-01-02 15:04:05 ")+format+"\n", args...)
	}
}

func (d *Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Run starts all enabled scheduled and watched jobs and blocks until ctx is
// done. Returns an error only if the job list cannot be loaded.
func (d *Daemon) Run(ctx context.Context) error {
	jobs, err := d.Store.LoadJobs()
	if err != nil {
		return err
	}
	d.running = map[string]bool{}
	d.pending = map[string]bool{}

	var wg sync.WaitGroup
	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		if job.Schedule != "" {
			sched, err := schedule.Parse(job.Schedule)
			if err != nil {
				d.logf("job %s: skipping schedule: %v", job.Name, err)
			} else {
				wg.Add(1)
				go func(j engine.Job, s *schedule.Schedule) {
					defer wg.Done()
					d.scheduleLoop(ctx, j, s)
				}(job, sched)
			}
		}
		if job.Watch {
			wg.Add(1)
			go func(j engine.Job) {
				defer wg.Done()
				d.watchLoop(ctx, j)
			}(job)
		}
	}
	d.logf("daemon started with %d enabled job(s)", len(jobs))
	<-ctx.Done()
	wg.Wait()
	return nil
}

// scheduleLoop fires a job at each cron tick. On startup it runs once if a
// tick was missed since the job's last recorded run.
func (d *Daemon) scheduleLoop(ctx context.Context, job engine.Job, sched *schedule.Schedule) {
	now := d.now()
	if runs, err := d.Store.LastRuns(); err == nil {
		if last, ok := runs[job.Name]; ok && !last.Started.IsZero() {
			if next := sched.Next(last.Started); !next.IsZero() && !next.After(now) {
				d.logf("job %s: missed run at %s, running now", job.Name, next.Format(time.RFC3339))
				d.trigger(ctx, job.Name)
			}
		}
	}
	for {
		next := sched.Next(d.now())
		if next.IsZero() {
			d.logf("job %s: schedule has no future runs", job.Name)
			return
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			d.trigger(ctx, job.Name)
		}
	}
}

// watchLoop runs a job after filesystem changes under its source settle.
func (d *Daemon) watchLoop(ctx context.Context, job engine.Job) {
	deb := schedule.NewDebouncer(WatchDebounce, WatchMaxWait, func() {
		d.trigger(ctx, job.Name)
	})
	defer deb.Stop()
	d.logf("job %s: watching %s", job.Name, job.Source)
	if err := schedule.WatchTree(ctx, job.Source, deb.Trigger); err != nil {
		d.logf("job %s: watch stopped: %v", job.Name, err)
	}
}

// trigger runs the named job now, or coalesces with a run already in progress
// (at most one follow-up run is queued).
func (d *Daemon) trigger(ctx context.Context, name string) {
	d.mu.Lock()
	if d.running[name] {
		d.pending[name] = true
		d.mu.Unlock()
		return
	}
	d.running[name] = true
	d.mu.Unlock()

	go func() {
		for {
			d.runOnce(ctx, name)
			d.mu.Lock()
			again := d.pending[name] && ctx.Err() == nil
			d.pending[name] = false
			if !again {
				d.running[name] = false
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
		}
	}()
}

func (d *Daemon) runOnce(ctx context.Context, name string) {
	job, err := d.Store.Job(name)
	if err != nil {
		d.logf("job %s: %v", name, err)
		return
	}
	started := d.now()
	res, err := engine.Run(ctx, job, engine.Options{
		Hasher: d.Hasher, Now: d.now, Secrets: d.Secrets,
	})
	rec := engine.RunRecord{
		Started: started, Finished: d.now(), Snapshot: res.Snapshot,
		OK: err == nil, Copied: res.Copied, Linked: res.Linked, Deleted: res.Deleted,
	}
	if err != nil {
		rec.Error = err.Error()
		d.logf("job %s: FAILED: %v", name, err)
	} else {
		d.logf("job %s: ok snapshot=%s copied=%d linked=%d", name, res.Snapshot, res.Copied, res.Linked)
	}
	if serr := d.Store.RecordRun(name, rec); serr != nil {
		d.logf("job %s: recording run: %v", name, serr)
	}
}
