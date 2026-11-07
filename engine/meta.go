// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tpt-solutions/tpt-sync/engine/backend"
	"github.com/tpt-solutions/tpt-sync/engine/crypt"
)

// On-destination layout (any backend):
//
//	.tpt-sync/latest                 name of the newest complete snapshot
//	.tpt-sync/versions/<ts>/...      one full logical tree per run
//	.tpt-sync/versions/<ts>/.manifest
//	                                 path → stored name, size, mtime for the snapshot
//
// Each snapshot is complete. On local destinations, files unchanged since the
// previous snapshot are hardlinked to it. Remote destinations copy them.
//
// Encrypted destinations additionally hold:
//
//	.tpt-sync/crypto.json            Argon2id parameters + sealed passphrase check
//
// where stored names are keyed hashes of the paths and the manifest is sealed.
// The manifest is what maps stored names back to paths.
const (
	metaDir      = ".tpt-sync"
	versionsDir  = ".tpt-sync/versions"
	latestFile   = ".tpt-sync/latest"
	cryptoFile   = ".tpt-sync/crypto.json"
	manifestName = ".manifest"
	tsLayout     = "20060102-150405.000000000"
)

// ErrNotEncrypted is returned when an encrypted operation targets a destination
// that has no encryption metadata.
var ErrNotEncrypted = errors.New("destination is not encrypted")

type cryptoMeta struct {
	Version int          `json:"version"`
	Params  crypt.Params `json:"params"`
	Check   []byte       `json:"check"`
}

// openKeys derives the destination's keys from passphrase. With create=true
// and no metadata yet, new parameters are generated and written. With
// create=false, missing metadata is an error.
func openKeys(ctx context.Context, dst backend.Backend, passphrase []byte, create bool) (*crypt.Keys, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("a passphrase is required for encrypted jobs")
	}
	r, err := dst.Open(ctx, cryptoFile)
	if errors.Is(err, backend.ErrNotExist) {
		if !create {
			return nil, ErrNotEncrypted
		}
		return initKeys(ctx, dst, passphrase)
	}
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var meta cryptoMeta
	if err := json.NewDecoder(r).Decode(&meta); err != nil {
		return nil, fmt.Errorf("reading %s: %w", cryptoFile, err)
	}
	keys, err := crypt.Derive(passphrase, meta.Params)
	if err != nil {
		return nil, err
	}
	if err := crypt.VerifyCheck(keys, meta.Check); err != nil {
		return nil, err
	}
	return keys, nil
}

func initKeys(ctx context.Context, dst backend.Backend, passphrase []byte) (*crypt.Keys, error) {
	params, err := crypt.NewParams()
	if err != nil {
		return nil, err
	}
	keys, err := crypt.Derive(passphrase, params)
	if err != nil {
		return nil, err
	}
	check, err := crypt.MakeCheck(keys)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(cryptoMeta{Version: 1, Params: params, Check: check}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := dst.Write(ctx, cryptoFile, bytes.NewReader(append(b, '\n')), time.Now()); err != nil {
		return nil, err
	}
	return keys, nil
}

// hasEncryptionMetadata reports whether the destination was initialised for encryption.
func hasEncryptionMetadata(ctx context.Context, dst backend.Backend) (bool, error) {
	_, err := dst.Stat(ctx, cryptoFile)
	if errors.Is(err, backend.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// storedName is the name a file's content is stored under in a snapshot.
// Plaintext destinations use the path itself; encrypted ones use a keyed hash.
func storedName(keys *crypt.Keys, rel string) string {
	if keys == nil {
		return rel
	}
	return keys.ObfName(rel)
}

// manifestEntry describes one file in a snapshot.
type manifestEntry struct {
	Path    string    `json:"path"`
	Stored  string    `json:"stored"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

type manifest struct {
	Version int             `json:"version"`
	Entries []manifestEntry `json:"entries"`
}

// writeManifest stores the snapshot's manifest, sealed when keys is non-nil.
func writeManifest(ctx context.Context, dst backend.Backend, keys *crypt.Keys, snap string, entries []manifestEntry) error {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	plain, err := json.Marshal(manifest{Version: 1, Entries: entries})
	if err != nil {
		return err
	}
	body := plain
	if keys != nil {
		if body, err = keys.Seal(plain); err != nil {
			return err
		}
	}
	return dst.Write(ctx, snap+"/"+manifestName, bytes.NewReader(body), time.Now())
}

// readManifest loads a snapshot's manifest, opening it with keys when non-nil.
func readManifest(ctx context.Context, dst backend.Backend, keys *crypt.Keys, snap string) (manifest, error) {
	r, err := dst.Open(ctx, snap+"/"+manifestName)
	if errors.Is(err, backend.ErrNotExist) {
		return manifest{}, fmt.Errorf("no such snapshot %q", snap)
	}
	if err != nil {
		return manifest{}, err
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return manifest{}, err
	}
	body := buf.Bytes()
	if keys != nil {
		if body, err = keys.Open(body); err != nil {
			return manifest{}, fmt.Errorf("snapshot %s manifest: %w", snap, err)
		}
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return manifest{}, fmt.Errorf("snapshot %s manifest: %w", snap, err)
	}
	return m, nil
}
