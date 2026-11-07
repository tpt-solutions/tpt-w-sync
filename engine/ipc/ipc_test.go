// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func testEndpoint(t *testing.T) string {
	t.Helper()
	if isWindows() {
		return fmt.Sprintf(`\\.\pipe\tpt-sync-test-%d`, time.Now().UnixNano())
	}
	return filepath.Join(t.TempDir(), "d.sock")
}

func startServer(t *testing.T, ep string, h Handler) context.CancelFunc {
	t.Helper()
	l, err := Listen(ep)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, h) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel
}

func TestRoundTrip(t *testing.T) {
	ep := testEndpoint(t)
	startServer(t, ep, func(_ context.Context, req Request) Response {
		data, _ := json.Marshal(map[string]string{"echo": req.Op + ":" + req.Job})
		return Response{OK: true, Data: data}
	})
	resp, err := Call(context.Background(), ep, Request{Op: "run", Job: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("response not OK: %+v", resp)
	}
	var got map[string]string
	if err := json.Unmarshal(resp.Data, &got); err != nil || got["echo"] != "run:docs" {
		t.Fatalf("data = %s, err = %v", resp.Data, err)
	}
}

func TestNotRunning(t *testing.T) {
	_, err := Call(context.Background(), testEndpoint(t), Request{Op: "ping"})
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestMalformedRequestGetsErrorResponse(t *testing.T) {
	ep := testEndpoint(t)
	startServer(t, ep, func(context.Context, Request) Response { return Response{OK: true} })
	conn, err := dial(ep, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("not json\n")); err != nil {
		t.Fatal(err)
	}
	line, err := readLine(newReader(conn))
	if err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil || resp.OK || resp.Error == "" {
		t.Fatalf("resp = %s, err = %v", line, err)
	}
}

func TestSecondListenerRefusedWhileFirstLive(t *testing.T) {
	if isWindows() {
		t.Skip("named pipes are exclusive by construction")
	}
	ep := testEndpoint(t)
	startServer(t, ep, func(context.Context, Request) Response { return Response{OK: true} })
	if _, err := Listen(ep); err == nil {
		t.Fatal("second listener took over a live socket")
	}
}

func isWindows() bool { return runtime.GOOS == "windows" }

func newReader(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }
