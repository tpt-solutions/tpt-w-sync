// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package ipc is the local control channel between the tpt-sync CLI (or GUI)
// and the running daemon. It uses a per-user named pipe on Windows and a
// Unix-domain socket elsewhere. The protocol is one JSON request and one JSON
// response per line on a single connection.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Request is a control operation sent by a client.
type Request struct {
	Op  string `json:"op"`            // "ping", "status" or "run"
	Job string `json:"job,omitempty"` // for "run"
}

// Response answers a Request. Data holds op-specific JSON when OK is true.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Handler serves one request.
type Handler func(ctx context.Context, req Request) Response

// ErrNotRunning means no daemon is listening on the endpoint.
var ErrNotRunning = errors.New("daemon is not running")

// maxLine bounds one request or response to protect the daemon from huge input.
const maxLine = 1 << 20

// Serve accepts connections on l until ctx is done, handling one request per
// connection. It closes l on return.
func Serve(ctx context.Context, l net.Listener, h Handler) error {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(ctx, conn, h)
	}
}

func serveConn(ctx context.Context, conn net.Conn, h Handler) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	line, err := readLine(bufio.NewReader(conn))
	var resp Response
	if err != nil {
		resp = Response{Error: "reading request: " + err.Error()}
	} else {
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			resp = Response{Error: "bad request: " + err.Error()}
		} else {
			resp = h(ctx, req)
		}
	}
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(out) > 0 {
				return out, nil
			}
			return nil, err
		}
		out = append(out, chunk...)
		if len(out) > maxLine {
			return nil, errors.New("message too large")
		}
		if !isPrefix {
			return out, nil
		}
	}
}

// Call sends req to the daemon at endpoint and waits for the response.
// It returns ErrNotRunning when nothing is listening.
func Call(ctx context.Context, endpoint string, req Request) (Response, error) {
	conn, err := dial(endpoint, 500*time.Millisecond)
	if err != nil {
		return Response{}, ErrNotRunning
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
	}
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return Response{}, err
	}
	line, err := readLine(bufio.NewReader(conn))
	if err != nil {
		return Response{}, fmt.Errorf("reading response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("bad response: %w", err)
	}
	return resp, nil
}
