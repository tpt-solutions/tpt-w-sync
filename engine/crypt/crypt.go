// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

// Package crypt implements tpt-sync's optional end-to-end encryption.
//
// Key derivation: Argon2id(passphrase, salt) yields a master key, from which
// independent sub-keys are derived with HMAC-SHA256 for content encryption and
// for obfuscating file names.
//
// File format (streaming, chunked AES-256-GCM):
//
//	magic "TPTE\x01" | 8-byte random nonce prefix | frame*
//	frame = uint32 BE ciphertext length | ciphertext (plaintext chunk + 16-byte tag)
//
// Each frame's nonce is prefix || uint32 BE chunk counter, and its AAD is a
// single byte: 1 for the final frame, 0 otherwise. Authenticating the counter
// and final flag makes reordering, truncation, and trailing data detectable.
package crypt

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// ErrWrongPassphrase means the passphrase does not match the destination.
var ErrWrongPassphrase = errors.New("wrong passphrase for this destination")

// ErrCorrupt means encrypted data failed authentication or is truncated.
var ErrCorrupt = errors.New("encrypted data is corrupt or truncated")

const (
	magic     = "TPTE\x01"
	prefixLen = 8
	chunkSize = 64 << 10
	tagLen    = 16
)

// Params are the Argon2id parameters and salt, stored in cleartext alongside
// the destination so the key can be re-derived.
type Params struct {
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory_kib"`
	Threads uint8  `json:"threads"`
	Salt    []byte `json:"salt"`
}

// NewParams returns parameters with a fresh random salt and conservative
// defaults (3 passes, 64 MiB, 4 lanes).
func NewParams() (Params, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Params{}, err
	}
	return Params{Time: 3, Memory: 64 * 1024, Threads: 4, Salt: salt}, nil
}

// Keys holds the derived sub-keys for one destination.
type Keys struct {
	content [32]byte
	names   [32]byte
}

// Derive runs Argon2id on passphrase with p and splits the result into keys.
func Derive(passphrase []byte, p Params) (*Keys, error) {
	if len(p.Salt) < 8 || p.Time == 0 || p.Memory == 0 || p.Threads == 0 {
		return nil, errors.New("crypt: invalid key parameters")
	}
	master := argon2.IDKey(passphrase, p.Salt, p.Time, p.Memory, p.Threads, 32)
	k := &Keys{}
	k.content = subkey(master, "content")
	k.names = subkey(master, "names")
	return k, nil
}

func subkey(master []byte, label string) [32]byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte("tpt-sync v1 " + label))
	var out [32]byte
	copy(out[:], m.Sum(nil))
	return out
}

func (k *Keys) gcm() cipher.AEAD {
	block, err := aes.NewCipher(k.content[:])
	if err != nil { // unreachable: key is always 32 bytes
		panic(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil { // unreachable: standard AES block size
		panic(err)
	}
	return g
}

// ObfName returns a stable, keyed name for a relative path. The same path
// always maps to the same name (so unchanged files can be hardlinked across
// snapshots), but the name reveals nothing about the path without the key.
func (k *Keys) ObfName(rel string) string {
	m := hmac.New(sha256.New, k.names[:])
	m.Write([]byte(rel))
	return hex.EncodeToString(m.Sum(nil)[:20])
}

// Seal encrypts a small blob (manifests, key checks) as nonce || ciphertext.
func (k *Keys) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.gcm().Seal(nonce, nonce, plain, []byte("blob")), nil
}

// Open reverses Seal.
func (k *Keys) Open(blob []byte) ([]byte, error) {
	if len(blob) < 12+tagLen {
		return nil, ErrCorrupt
	}
	pt, err := k.gcm().Open(nil, blob[:12], blob[12:], []byte("blob"))
	if err != nil {
		return nil, ErrCorrupt
	}
	return pt, nil
}

// MakeCheck returns a sealed known value used to verify a passphrase later.
func MakeCheck(k *Keys) ([]byte, error) { return k.Seal([]byte("tpt-sync passphrase check")) }

// VerifyCheck returns ErrWrongPassphrase unless k opens the check blob.
func VerifyCheck(k *Keys, check []byte) error {
	pt, err := k.Open(check)
	if err != nil || string(pt) != "tpt-sync passphrase check" {
		return ErrWrongPassphrase
	}
	return nil
}

// EncryptStream reads plaintext from src and writes the encrypted stream to dst.
func (k *Keys) EncryptStream(dst io.Writer, src io.Reader) error {
	var prefix [prefixLen]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return err
	}
	if _, err := dst.Write(append([]byte(magic), prefix[:]...)); err != nil {
		return err
	}
	g := k.gcm()
	br := bufio.NewReaderSize(src, chunkSize)
	buf := make([]byte, chunkSize)
	var counter uint32
	for {
		n, err := io.ReadFull(br, buf)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		last := err != nil // a short read means the input ended
		if !last {
			if _, perr := br.Peek(1); errors.Is(perr, io.EOF) {
				last = true
			} else if perr != nil {
				return perr
			}
		}
		if err := writeFrame(dst, g, prefix[:], counter, last, buf[:n]); err != nil {
			return err
		}
		if last {
			return nil
		}
		if counter == ^uint32(0) {
			return errors.New("crypt: input too large")
		}
		counter++
	}
}

func writeFrame(dst io.Writer, g cipher.AEAD, prefix []byte, counter uint32, last bool, plain []byte) error {
	nonce := frameNonce(prefix, counter)
	ct := g.Seal(nil, nonce, plain, frameAAD(last))
	var lenb [4]byte
	binary.BigEndian.PutUint32(lenb[:], uint32(len(ct)))
	if _, err := dst.Write(lenb[:]); err != nil {
		return err
	}
	_, err := dst.Write(ct)
	return err
}

// DecryptStream reads an encrypted stream from src and writes plaintext to dst.
// Output written before an authentication failure must be discarded by the
// caller; the error reports that the stream is corrupt or truncated.
func (k *Keys) DecryptStream(dst io.Writer, src io.Reader) error {
	hdr := make([]byte, len(magic)+prefixLen)
	if _, err := io.ReadFull(src, hdr); err != nil || string(hdr[:len(magic)]) != magic {
		return ErrCorrupt
	}
	prefix := hdr[len(magic):]
	g := k.gcm()
	var counter uint32
	for {
		var lenb [4]byte
		if _, err := io.ReadFull(src, lenb[:]); err != nil {
			return ErrCorrupt
		}
		n := binary.BigEndian.Uint32(lenb[:])
		if n < tagLen || n > chunkSize+tagLen {
			return ErrCorrupt
		}
		ct := make([]byte, n)
		if _, err := io.ReadFull(src, ct); err != nil {
			return ErrCorrupt
		}
		nonce := frameNonce(prefix, counter)
		pt, err := g.Open(nil, nonce, ct, frameAAD(false))
		last := false
		if err != nil {
			pt, err = g.Open(nil, nonce, ct, frameAAD(true))
			if err != nil {
				return ErrCorrupt
			}
			last = true
		}
		if _, err := dst.Write(pt); err != nil {
			return err
		}
		if last {
			var one [1]byte
			if m, _ := src.Read(one[:]); m > 0 {
				return ErrCorrupt // trailing data after the final frame
			}
			return nil
		}
		counter++
	}
}

func frameNonce(prefix []byte, counter uint32) []byte {
	nonce := make([]byte, 0, 12)
	nonce = append(nonce, prefix...)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], counter)
	return append(nonce, c[:]...)
}

func frameAAD(last bool) []byte {
	if last {
		return []byte{1}
	}
	return []byte{0}
}

// EncryptBytes and DecryptBytes are whole-buffer conveniences.
func (k *Keys) EncryptBytes(plain []byte) ([]byte, error) {
	var b bytes.Buffer
	if err := k.EncryptStream(&b, bytes.NewReader(plain)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// DecryptBytes reverses EncryptBytes.
func (k *Keys) DecryptBytes(enc []byte) ([]byte, error) {
	var b bytes.Buffer
	if err := k.DecryptStream(&b, bytes.NewReader(enc)); err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return b.Bytes(), nil
}
