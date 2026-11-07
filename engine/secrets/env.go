// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package secrets

import (
	"os"
	"strings"
)

// PassphraseEnv is the environment variable that overrides stored passphrases.
// Useful for unattended runs (CI, scripts); the value is visible to other
// processes of the same user, so prefer the keychain for daemons.
const PassphraseEnv = "TPT_SYNC_PASSPHRASE"

// EnvOverride wraps a Store so that, when PassphraseEnv is set, every
// passphrase lookup returns its value. All other accounts go to Next.
type EnvOverride struct {
	Next Store
}

// Get returns the environment passphrase for passphrase accounts, else Next.
func (e EnvOverride) Get(account string) (string, error) {
	if strings.HasSuffix(account, "/passphrase") {
		if v := os.Getenv(PassphraseEnv); v != "" {
			return v, nil
		}
	}
	return e.Next.Get(account)
}

// Set delegates to Next.
func (e EnvOverride) Set(account, value string) error { return e.Next.Set(account, value) }

// Delete delegates to Next.
func (e EnvOverride) Delete(account string) error { return e.Next.Delete(account) }
