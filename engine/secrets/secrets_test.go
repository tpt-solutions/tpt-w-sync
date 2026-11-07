// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package secrets

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMemoryStore(t *testing.T) {
	s := NewMemory()
	if _, err := s.Get("x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
	_ = s.Set("x", "v1")
	_ = s.Set("x", "v2")
	if v, _ := s.Get("x"); v != "v2" {
		t.Fatalf("got %q", v)
	}
	_ = s.Delete("x")
	if _, err := s.Get("x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete did not remove secret")
	}
}

// Exercises the real OS credential store under a unique account name, and
// cleans up afterwards. Skipped when no credential store is available.
func TestKeychainRoundTrip(t *testing.T) {
	account := fmt.Sprintf("tpt-sync-test/%d", time.Now().UnixNano())
	var kc Keychain
	if err := kc.Set(account, "s3cret value"); err != nil {
		t.Skipf("no OS credential store available: %v", err)
	}
	defer kc.Delete(account)

	got, err := kc.Get(account)
	if err != nil || got != "s3cret value" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := kc.Delete(account); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get(account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := kc.Delete(account); err != nil {
		t.Fatalf("deleting a missing secret should succeed: %v", err)
	}
}
