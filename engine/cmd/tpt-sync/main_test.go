// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine"
	"github.com/tpt-solutions/tpt-sync/engine/daemon"
)

// invoke runs the CLI with a private config directory and returns stdout.
func invoke(t *testing.T, cfg string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(append([]string{"--config", cfg}, args...), &out, &errOut)
	return out.String(), err
}

func mustInvoke(t *testing.T, cfg string, args ...string) string {
	t.Helper()
	out, err := invoke(t, cfg, args...)
	if err != nil {
		t.Fatalf("tpt-sync %v: %v", args, err)
	}
	return out
}

func fixture(t *testing.T) (cfg, src, dst string) {
	t.Helper()
	base := t.TempDir()
	src = filepath.Join(base, "src")
	dst = filepath.Join(base, "dst")
	cfg = filepath.Join(base, "cfg")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "note.txt"), []byte("note"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, src, dst
}

func TestJobLifecycle(t *testing.T) {
	cfg, src, dst := fixture(t)
	out := mustInvoke(t, cfg, "job", "add", "docs", "--source", src, "--dest", dst,
		"--exclude", "*.tmp", "--max-versions", "3")
	if !strings.Contains(out, "added job docs") {
		t.Fatalf("add output: %q", out)
	}
	if _, err := invoke(t, cfg, "job", "add", "docs", "--source", src, "--dest", dst); err == nil {
		t.Fatal("duplicate job name accepted")
	}

	list := mustInvoke(t, cfg, "job", "list")
	if !strings.Contains(list, "docs") || !strings.Contains(list, dst) {
		t.Fatalf("list output: %q", list)
	}

	show := mustInvoke(t, cfg, "job", "show", "docs")
	if !strings.Contains(show, `"max_versions": 3`) || !strings.Contains(show, `"type": "local"`) {
		t.Fatalf("show output: %q", show)
	}

	mustInvoke(t, cfg, "job", "edit", "docs", "--max-versions", "7")
	if show := mustInvoke(t, cfg, "job", "show", "docs"); !strings.Contains(show, `"max_versions": 7`) {
		t.Fatal("edit did not apply")
	}
	if show := mustInvoke(t, cfg, "job", "show", "docs"); !strings.Contains(show, `"source": "`) {
		t.Fatal("edit dropped unrelated fields")
	}

	mustInvoke(t, cfg, "schedule", "disable", "docs")
	if list := mustInvoke(t, cfg, "job", "list"); !strings.Contains(list, "false") {
		t.Fatalf("disable not reflected: %q", list)
	}

	mustInvoke(t, cfg, "job", "remove", "docs")
	if _, err := invoke(t, cfg, "job", "show", "docs"); err == nil {
		t.Fatal("removed job still shown")
	}
}

func TestRunDryRunThenRealRun(t *testing.T) {
	cfg, src, dst := fixture(t)
	mustInvoke(t, cfg, "job", "add", "docs", "--source", src, "--dest", dst)

	plan := mustInvoke(t, cfg, "run", "docs", "--dry-run")
	if !strings.Contains(plan, "create") || !strings.Contains(plan, "dry run: 1 create") {
		t.Fatalf("dry run output: %q", plan)
	}
	if _, err := os.Stat(filepath.Join(dst, ".tpt-sync")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote to destination")
	}

	out := mustInvoke(t, cfg, "run", "docs")
	if !strings.Contains(out, "snapshot ") || !strings.Contains(out, "1 copied") {
		t.Fatalf("run output: %q", out)
	}
	if v := mustInvoke(t, cfg, "versions", "list", "docs"); !strings.Contains(v, "(latest)") {
		t.Fatalf("versions output: %q", v)
	}

	// The run was recorded, so status reports it.
	st := mustInvoke(t, cfg, "status")
	if !strings.Contains(st, "docs") || !strings.Contains(st, "ok") {
		t.Fatalf("status output: %q", st)
	}

	restore := filepath.Join(t.TempDir(), "restored")
	mustInvoke(t, cfg, "versions", "restore", "docs", "--path", "sub", "--to", restore)
	b, err := os.ReadFile(filepath.Join(restore, "sub", "note.txt"))
	if err != nil || string(b) != "note" {
		t.Fatalf("restored content = %q, %v", b, err)
	}
}

func TestRunDelegatesToRunningDaemon(t *testing.T) {
	cfg, src, dst := fixture(t)
	mustInvoke(t, cfg, "job", "add", "docs", "--source", src, "--dest", dst)

	store := engine.Store{Dir: cfg}
	endpoint := (&cli{store: store}).endpoint()
	d := &daemon.Daemon{Store: store, ControlEndpoint: endpoint}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// Wait until the control channel answers.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if out, err := invoke(t, cfg, "status"); err == nil && strings.Contains(out, "RUNNING") {
			if strings.Contains(out, "yes") || strings.Contains(out, "no") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon control channel did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	out := mustInvoke(t, cfg, "run", "docs")
	if !strings.Contains(out, "run by daemon") {
		t.Fatalf("run did not go through the daemon: %q", out)
	}
	// A second daemon for the same config must not take over the endpoint.
	if err := (&daemon.Daemon{Store: store, ControlEndpoint: endpoint}).Run(context.Background()); err == nil {
		t.Fatal("second daemon started on a live control channel")
	}
}

func TestUsageErrors(t *testing.T) {
	cfg, _, _ := fixture(t)
	if out, err := invoke(t, cfg); err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("no args should print usage: %v %q", err, out)
	}
	if _, err := invoke(t, cfg, "bogus"); err == nil {
		t.Fatal("unknown command accepted")
	}
	if _, err := invoke(t, cfg, "job", "add"); err == nil {
		t.Fatal("job add without name accepted")
	}
	if _, err := invoke(t, cfg, "job", "add", "x"); err == nil {
		t.Fatal("job add without destination accepted")
	}
	if _, err := invoke(t, cfg, "secret", "set", "x"); err == nil {
		t.Fatal("malformed secret command accepted")
	}
	if _, err := invoke(t, cfg, "run", "missing"); err == nil {
		t.Fatal("run of unknown job accepted")
	}
}

func TestRemoteDestinationFlagsInferType(t *testing.T) {
	cfg, src, _ := fixture(t)
	mustInvoke(t, cfg, "job", "add", "cloud", "--source", src, "--url", "https://dav.example.com/x", "--username", "u")
	mustInvoke(t, cfg, "job", "add", "bucket", "--source", src, "--bucket", "b", "--dest", "pc1")

	show := mustInvoke(t, cfg, "job", "show", "cloud")
	if !strings.Contains(show, `"type": "webdav"`) {
		t.Fatalf("webdav not inferred: %s", show)
	}
	show = mustInvoke(t, cfg, "job", "show", "bucket")
	if !strings.Contains(show, `"type": "s3"`) {
		t.Fatalf("s3 not inferred: %s", show)
	}
}
