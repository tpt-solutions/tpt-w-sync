// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package secrets stores credentials (destination passwords, access keys and
// encryption passphrases) in the operating system's credential store:
// Windows Credential Manager, macOS Keychain, or the Linux Secret Service.
package secrets

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name all tpt-sync secrets are filed under.
const Service = "tpt-sync"

// ErrNotFound means no secret is stored under the requested account.
var ErrNotFound = errors.New("secret not found")

// Store is a secret store keyed by account name, e.g. "<job>/passphrase".
type Store interface {
	Get(account string) (string, error)
	Set(account, value string) error
	Delete(account string) error
}

// Keychain is the OS credential store.
type Keychain struct{}

// Get returns the secret for account.
func (Keychain) Get(account string) (string, error) {
	v, err := keyring.Get(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

// Set stores or replaces the secret for account.
func (Keychain) Set(account, value string) error {
	return keyring.Set(Service, account, value)
}

// Delete removes the secret for account. Deleting a missing secret is not an error.
func (Keychain) Delete(account string) error {
	err := keyring.Delete(Service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-process Store for tests and for runs that must not touch
// the OS keychain.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

// Get returns the secret for account.
func (s *Memory) Get(account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[account]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores the secret for account.
func (s *Memory) Set(account, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[account] = value
	return nil
}

// Delete removes account.
func (s *Memory) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, account)
	return nil
}

// Account names used by the engine. Each is scoped to a job name.
func PassphraseAccount(job string) string { return job + "/passphrase" }

// PasswordAccount is the account for a remote destination's password or secret key.
func PasswordAccount(job string) string { return job + "/password" }
