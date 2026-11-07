// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"time"
)

// DefaultEndpoint returns the socket path for a config directory.
func DefaultEndpoint(configDir string) string {
	return filepath.Join(configDir, "daemon.sock")
}

// Listen creates a Unix socket readable only by the current user. A stale
// socket file from a crashed daemon is removed first.
func Listen(endpoint string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(endpoint), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(endpoint); err == nil {
		if c, derr := net.DialTimeout("unix", endpoint, 200*time.Millisecond); derr == nil {
			c.Close()
			return nil, os.ErrExist // another daemon is live
		}
		_ = os.Remove(endpoint)
	}
	l, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(endpoint, 0o600)
	return l, nil
}

func dial(endpoint string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", endpoint, timeout)
}
