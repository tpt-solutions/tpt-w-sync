// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package crypt

import (
	"bytes"
	"testing"
)

// FuzzDecryptStream feeds arbitrary bytes to the decrypter. It must never
// panic. Authentication failures are covered deterministically by the
// tamper and truncation tests; the fuzzer exercises the parser's edge cases.
func FuzzDecryptStream(f *testing.F) {
	p := Params{Time: 1, Memory: 1024, Threads: 1, Salt: []byte("fuzz-salt-1234")}
	k, err := Derive([]byte("fuzz"), p)
	if err != nil {
		f.Fatal(err)
	}
	enc, err := k.EncryptBytes([]byte("seed payload"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(enc)
	f.Add([]byte("TPTE\x01"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		var out bytes.Buffer
		_ = k.DecryptStream(&out, bytes.NewReader(data))
	})
}
