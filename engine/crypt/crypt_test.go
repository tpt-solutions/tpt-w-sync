// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package crypt

import (
	"bytes"
	"errors"
	"testing"
)

func testKeys(t *testing.T, pass string) *Keys {
	t.Helper()
	p, err := NewParams()
	if err != nil {
		t.Fatal(err)
	}
	// Reduced cost keeps the test suite fast; defaults are exercised in NewParams.
	p.Time, p.Memory, p.Threads = 1, 1024, 1
	k, err := Derive([]byte(pass), p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRoundTripAtChunkBoundaries(t *testing.T) {
	k := testKeys(t, "correct horse")
	for _, n := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 7} {
		plain := make([]byte, n)
		for i := range plain {
			plain[i] = byte(i * 31)
		}
		enc, err := k.EncryptBytes(plain)
		if err != nil {
			t.Fatalf("n=%d encrypt: %v", n, err)
		}
		dec, err := k.DecryptBytes(enc)
		if err != nil {
			t.Fatalf("n=%d decrypt: %v", n, err)
		}
		if !bytes.Equal(dec, plain) {
			t.Fatalf("n=%d round trip mismatch", n)
		}
	}
}

func TestCiphertextDiffersFromPlaintextAndAcrossRuns(t *testing.T) {
	k := testKeys(t, "pw")
	plain := []byte("the same content every time")
	a, _ := k.EncryptBytes(plain)
	b, _ := k.EncryptBytes(plain)
	if bytes.Contains(a, plain) {
		t.Fatal("plaintext visible in ciphertext")
	}
	if bytes.Equal(a, b) {
		t.Fatal("identical ciphertext for identical input (nonce reuse)")
	}
}

func TestTamperingIsDetected(t *testing.T) {
	k := testKeys(t, "pw")
	plain := make([]byte, 2*chunkSize+100)
	enc, _ := k.EncryptBytes(plain)

	// Flip a byte in the middle of the first frame's ciphertext.
	bad := append([]byte(nil), enc...)
	bad[len(magic)+prefixLen+4+10] ^= 0x01
	if _, err := k.DecryptBytes(bad); err == nil {
		t.Fatal("bit flip not detected")
	}
}

func TestTruncationAndTrailingDataDetected(t *testing.T) {
	k := testKeys(t, "pw")
	plain := make([]byte, 2*chunkSize+100)
	enc, _ := k.EncryptBytes(plain)

	if _, err := k.DecryptBytes(enc[:len(enc)-5]); err == nil {
		t.Fatal("truncated stream accepted")
	}
	// Dropping whole trailing frames must not look like a valid shorter file.
	frame1End := len(magic) + prefixLen + 4 + chunkSize + tagLen
	if _, err := k.DecryptBytes(enc[:frame1End]); err == nil {
		t.Fatal("stream cut after a non-final frame accepted")
	}
	extra := append(append([]byte(nil), enc...), 0x00)
	if _, err := k.DecryptBytes(extra); err == nil {
		t.Fatal("trailing garbage accepted")
	}
}

func TestWrongPassphraseRejectedByCheck(t *testing.T) {
	p, _ := NewParams()
	p.Time, p.Memory, p.Threads = 1, 1024, 1
	right, _ := Derive([]byte("right"), p)
	check, err := MakeCheck(right)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCheck(right, check); err != nil {
		t.Fatalf("right passphrase rejected: %v", err)
	}
	wrong, _ := Derive([]byte("wrong"), p)
	if err := VerifyCheck(wrong, check); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase: err = %v", err)
	}
}

func TestObfNameStableAndKeyed(t *testing.T) {
	k1 := testKeys(t, "a")
	k2 := testKeys(t, "b")
	if k1.ObfName("docs/a.txt") != k1.ObfName("docs/a.txt") {
		t.Fatal("name not deterministic")
	}
	if k1.ObfName("docs/a.txt") == k1.ObfName("docs/b.txt") {
		t.Fatal("different paths share a name")
	}
	if k1.ObfName("docs/a.txt") == k2.ObfName("docs/a.txt") {
		t.Fatal("name does not depend on the key")
	}
}

func TestDeriveRejectsBadParams(t *testing.T) {
	if _, err := Derive([]byte("x"), Params{}); err == nil {
		t.Fatal("zero params accepted")
	}
}
