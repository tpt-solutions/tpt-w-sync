// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine"
	"github.com/tpt-solutions/tpt-sync/engine/ipc"
	"github.com/tpt-solutions/tpt-sync/engine/schedule"
)

// fakeHasher avoids depending on the Rust helper in daemon tests.
type fakeHasher struct{}

func (fakeHasher) Hash(_ context.Context, paths []string) ([]engine.HashResult, error) {
	out := make([]engine.HashResult, len(paths))
	for i, p := range paths {
		b, err := os.ReadFile(p)
		out[i] = engine.HashResult{Path: p, Sum: string(b), Err: err}
	}
	return out, nil
}

func setup(t *testing.T) (engine.Store, string) {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := engine.Store{Dir: filepath.Join(base, "cfg")}
	job := engine.Job{
		Name: "docs", Source: src, Enabled: true,
		Destination: engine.Destination{Type: "local", Path: filepath.Join(base, "dst")},
	}
	if err := store.SaveJobs([]engine.Job{job}); err != nil {
		t.Fatal(err)
	}
	return store, base
}

func TestMissedRun(t *testing.T) {
	sched, err := schedule.Parse("0 2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.Local)
	// Last ran yesterday at 02:00; now is 03:00 today: today's 02:00 was missed.
	last := day.AddDate(0, 0, -1).Add(2 * time.Hour)
	now := day.Add(3 * time.Hour)
	if tick, ok := MissedRun(sched, last, now); !ok || !tick.Equal(day.Add(2*time.Hour)) {
		t.Fatalf("missed = %v, %v", tick, ok)
	}
	// Ran today at 02:00; now is 03:00: nothing missed.
	if _, ok := MissedRun(sched, day.Add(2*time.Hour), now); ok {
		t.Fatal("reported a miss after a run that covered the tick")
	}
	// Never ran: no catch-up.
	if _, ok := MissedRun(sched, time.Time{}, now); ok {
		t.Fatal("reported a miss for a job that never ran")
	}
	// Before today's tick: not yet missed.
	if _, ok := MissedRun(sched, last, day.Add(time.Hour)); ok {
		t.Fatal("tick reported missed before it was due")
	}
}

func TestRunNowRecordsAndReturnsOutcome(t *testing.T) {
	store, _ := setup(t)
	d := &Daemon{Store: store, Hasher: fakeHasher{}}
	rec, err := d.RunNow(context.Background(), "docs")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.OK || rec.Snapshot == "" || rec.Copied != 1 {
		t.Fatalf("rec = %+v", rec)
	}
	runs, _ := store.LastRuns()
	if _, ok := runs["docs"]; !ok {
		t.Fatal("run was not recorded")
	}
}

func TestRunNowUnknownJob(t *testing.T) {
	store, _ := setup(t)
	d := &Daemon{Store: store, Hasher: fakeHasher{}}
	if _, err := d.RunNow(context.Background(), "nope"); err == nil {
		t.Fatal("unknown job accepted")
	}
}

// Concurrent RunNow calls for one job must not overlap: each waits for the
// previous run, so every call produces its own snapshot.
func TestConcurrentRunNowSerialises(t *testing.T) {
	store, base := setup(t)
	d := &Daemon{Store: store, Hasher: fakeHasher{}}
	var wg sync.WaitGroup
	snaps := make(chan string, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, err := d.RunNow(context.Background(), "docs")
			if err != nil || !rec.OK {
				t.Errorf("run failed: %v %+v", err, rec)
				return
			}
			snaps <- rec.Snapshot
		}()
	}
	wg.Wait()
	close(snaps)
	seen := map[string]bool{}
	for s := range snaps {
		if seen[s] {
			t.Fatalf("snapshot %s produced twice", s)
		}
		seen[s] = true
	}
	if len(seen) != 3 {
		t.Fatalf("got %d snapshots, want 3 (base %s)", len(seen), base)
	}
}

// End to end over the control channel: a client asks the running daemon to
// run a job and reads status back.
func TestControlChannelRunAndStatus(t *testing.T) {
	store, base := setup(t)
	endpoint := ipc.DefaultEndpoint(filepath.Join(base, "ctl"))
	if isWindowsPipe(endpoint) {
		endpoint = endpoint + "-" + time.Now().Format("150405.000000000")
	}
	d := &Daemon{Store: store, Hasher: fakeHasher{}, ControlEndpoint: endpoint}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	defer func() { cancel(); <-done }()

	var resp ipc.Response
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err = ipc.Call(context.Background(), endpoint, ipc.Request{Op: "run", Job: "docs"})
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || !resp.OK {
		t.Fatalf("run over control channel: %+v, %v", resp, err)
	}
	var rec engine.RunRecord
	if err := json.Unmarshal(resp.Data, &rec); err != nil || rec.Snapshot == "" {
		t.Fatalf("bad record %s: %v", resp.Data, err)
	}

	st, err := ipc.Call(context.Background(), endpoint, ipc.Request{Op: "status"})
	if err != nil || !st.OK {
		t.Fatalf("status: %+v %v", st, err)
	}
	var statuses []JobStatus
	if err := json.Unmarshal(st.Data, &statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Running || statuses[0].Last == nil || !statuses[0].Last.OK {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func isWindowsPipe(ep string) bool { return len(ep) > 9 && ep[:9] == `\\.\pipe\` }
