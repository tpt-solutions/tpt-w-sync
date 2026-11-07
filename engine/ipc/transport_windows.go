// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package ipc

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

// DefaultEndpoint returns the named pipe used for a config directory. The
// name includes a hash of the directory so separate configs do not collide.
func DefaultEndpoint(configDir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(configDir))))
	return `\\.\pipe\tpt-sync-` + hex.EncodeToString(sum[:6])
}

// Listen creates the named pipe. Default pipe security limits access to the
// creating user and administrators.
func Listen(endpoint string) (net.Listener, error) {
	return winio.ListenPipe(endpoint, nil)
}

func dial(endpoint string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(endpoint, &timeout)
}
