// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package daemon hosts the scheduler: it runs enabled jobs on their cron
// schedules and on filesystem change (watch) triggers, independent of any UI.
// It also serves a local control channel (package ipc) so the CLI and GUI can
// request runs and read status while the daemon owns the run state.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine"
	"github.com/tpt-solutions/tpt-sync/engine/ipc"
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
	// ControlEndpoint, if set, is where the control channel listens
	// (see ipc.DefaultEndpoint). Empty disables it.
	ControlEndpoint string

	mu     sync.Mutex
	states map[string]*jobState
}

// jobState serialises runs of one job. A trigger that arrives during a run
// marks it pending so exactly one follow-up run happens afterwards.
type jobState struct {
	cond    *sync.Cond
	running bool
	pending bool
	last    *engine.RunRecord
}

// JobStatus is the live state of one job, reported over the control channel.
type JobStatus struct {
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Running bool              `json:"running"`
	Last    *engine.RunRecord `json:"last,omitempty"`
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

// state returns the run state for a job, creating it on first use.
func (d *Daemon) state(name string) *jobState {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.states == nil {
		d.states = map[string]*jobState{}
	}
	st, ok := d.states[name]
	if !ok {
		st = &jobState{}
		st.cond = sync.NewCond(&d.mu)
		d.states[name] = st
	}
	return st
}

// Run starts scheduled and watched jobs and the control channel, and blocks
// until ctx is done. Returns an error if the job list cannot be loaded or the
// control endpoint cannot be opened.
func (d *Daemon) Run(ctx context.Context) error {
	jobs, err := d.Store.LoadJobs()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	if d.ControlEndpoint != "" {
		l, err := ipc.Listen(d.ControlEndpoint)
		if err != nil {
			return fmt.Errorf("control channel: %w", err)
		}
		d.logf("control channel at %s", d.ControlEndpoint)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ipc.Serve(ctx, l, d.handle); err != nil {
				d.logf("control channel stopped: %v", err)
			}
		}()
	}
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

// MissedRun reports whether a scheduled tick fell between the last recorded
// run and now, and if so returns that tick. A job that has never run is not
// considered to have missed anything.
func MissedRun(sched *schedule.Schedule, lastStarted, now time.Time) (time.Time, bool) {
	if lastStarted.IsZero() {
		return time.Time{}, false
	}
	next := sched.Next(lastStarted)
	if next.IsZero() || next.After(now) {
		return time.Time{}, false
	}
	return next, true
}

// scheduleLoop fires a job at each cron tick. On startup it runs once if a
// tick was missed since the job's last recorded run.
func (d *Daemon) scheduleLoop(ctx context.Context, job engine.Job, sched *schedule.Schedule) {
	if runs, err := d.Store.LastRuns(); err == nil {
		if last, ok := runs[job.Name]; ok {
			if tick, missed := MissedRun(sched, last.Started, d.now()); missed {
				d.logf("job %s: missed run at %s, running now", job.Name, tick.Format(time.RFC3339))
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

// trigger starts the named job in the background, or marks a run in progress
// as pending so one follow-up run happens when it finishes.
func (d *Daemon) trigger(ctx context.Context, name string) {
	st := d.state(name)
	d.mu.Lock()
	if st.running {
		st.pending = true
		d.mu.Unlock()
		return
	}
	st.running = true
	d.mu.Unlock()

	go func() {
		for {
			d.runOnce(ctx, name)
			d.mu.Lock()
			again := st.pending && ctx.Err() == nil
			st.pending = false
			if !again {
				st.running = false
				st.cond.Broadcast()
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
		}
	}()
}

// RunNow runs the named job and returns its outcome. If the job is already
// running, RunNow waits for that run to finish first, then runs.
func (d *Daemon) RunNow(ctx context.Context, name string) (engine.RunRecord, error) {
	if _, err := d.Store.Job(name); err != nil {
		return engine.RunRecord{}, err
	}
	st := d.state(name)
	d.mu.Lock()
	for st.running {
		st.cond.Wait()
	}
	st.running = true
	d.mu.Unlock()

	rec := d.runOnce(ctx, name)

	d.mu.Lock()
	st.running = false
	st.cond.Broadcast()
	d.mu.Unlock()
	return rec, nil
}

// runOnce performs one run, records it, and returns the outcome.
func (d *Daemon) runOnce(ctx context.Context, name string) engine.RunRecord {
	job, err := d.Store.Job(name)
	if err != nil {
		d.logf("job %s: %v", name, err)
		return engine.RunRecord{Started: d.now(), Finished: d.now(), Error: err.Error()}
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
	d.mu.Lock()
	st := d.states[name]
	if st == nil {
		d.mu.Unlock()
		return rec
	}
	st.last = &rec
	d.mu.Unlock()
	return rec
}

// Statuses reports the live state of every job in the store.
func (d *Daemon) Statuses() ([]JobStatus, error) {
	jobs, err := d.Store.LoadJobs()
	if err != nil {
		return nil, err
	}
	runs, err := d.Store.LastRuns()
	if err != nil {
		return nil, err
	}
	out := make([]JobStatus, 0, len(jobs))
	for _, j := range jobs {
		st := d.state(j.Name)
		d.mu.Lock()
		s := JobStatus{Name: j.Name, Enabled: j.Enabled, Running: st.running}
		if r, ok := runs[j.Name]; ok {
			r := r
			s.Last = &r
		}
		d.mu.Unlock()
		out = append(out, s)
	}
	return out, nil
}

// handle serves control-channel requests.
func (d *Daemon) handle(ctx context.Context, req ipc.Request) ipc.Response {
	switch req.Op {
	case "ping":
		return ipc.Response{OK: true}
	case "status":
		s, err := d.Statuses()
		if err != nil {
			return ipc.Response{Error: err.Error()}
		}
		return jsonResponse(s)
	case "run":
		rec, err := d.RunNow(ctx, req.Job)
		if err != nil {
			return ipc.Response{Error: err.Error()}
		}
		if !rec.OK {
			return ipc.Response{Error: rec.Error, Data: mustJSON(rec)}
		}
		return jsonResponse(rec)
	default:
		return ipc.Response{Error: fmt.Sprintf("unknown operation %q", req.Op)}
	}
}

func jsonResponse(v any) ipc.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return ipc.Response{Error: err.Error()}
	}
	return ipc.Response{OK: true, Data: b}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
