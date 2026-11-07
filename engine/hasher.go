// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// HashResult is the BLAKE3 digest (or error) for one path.
type HashResult struct {
	Path string
	Sum  string
	Err  error
}

// Hasher computes content checksums for local files. Results are returned in
// the same order as paths.
type Hasher interface {
	Hash(ctx context.Context, paths []string) ([]HashResult, error)
}

// HashHelperEnv overrides discovery of the tpt-sync-hash binary.
const HashHelperEnv = "TPT_SYNC_HASH"

// hashBatchSize bounds how many paths are sent to one helper invocation.
const hashBatchSize = 512

// ExecHasher runs the Rust tpt-sync-hash helper as a subprocess in batch mode
// (--stdin), so process-spawn cost is amortised across many files.
type ExecHasher struct {
	Binary string
}

// FindHashHelper locates tpt-sync-hash: $TPT_SYNC_HASH, then next to the
// running executable, then PATH.
func FindHashHelper() (string, error) {
	name := "tpt-sync-hash"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if env := os.Getenv(HashHelperEnv); env != "" {
		return env, nil
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found: set %s or place it next to tpt-sync", name, HashHelperEnv)
}

// Hash digests paths via the helper, in batches.
func (h ExecHasher) Hash(ctx context.Context, paths []string) ([]HashResult, error) {
	bin := h.Binary
	if bin == "" {
		var err error
		if bin, err = FindHashHelper(); err != nil {
			return nil, err
		}
	}
	out := make([]HashResult, 0, len(paths))
	for start := 0; start < len(paths); start += hashBatchSize {
		end := min(start+hashBatchSize, len(paths))
		res, err := runHelper(ctx, bin, paths[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, res...)
	}
	return out, nil
}

type helperRecord struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	Error string `json:"error"`
}

func runHelper(ctx context.Context, bin string, paths []string) ([]HashResult, error) {
	results := make([]HashResult, len(paths))
	// The helper reads one path per line, so paths with newlines cannot be sent.
	var input bytes.Buffer
	var sendIdx []int
	for i, p := range paths {
		results[i].Path = p
		if strings.ContainsAny(p, "\r\n") {
			results[i].Err = errors.New("path contains a newline; cannot hash")
			continue
		}
		input.WriteString(p)
		input.WriteByte('\n')
		sendIdx = append(sendIdx, i)
	}
	if len(sendIdx) == 0 {
		return results, nil
	}

	cmd := exec.CommandContext(ctx, bin, "--stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	// Write stdin concurrently so a large batch cannot deadlock on full pipes.
	go func() {
		_, _ = io.Copy(stdin, &input)
		_ = stdin.Close()
	}()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		if n >= len(sendIdx) {
			return nil, fmt.Errorf("hash helper produced more output lines than inputs")
		}
		var rec helperRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("hash helper output: %w", err)
		}
		i := sendIdx[n]
		if rec.Path != paths[i] {
			return nil, fmt.Errorf("hash helper out of order: got %q, want %q", rec.Path, paths[i])
		}
		switch {
		case rec.Error != "":
			results[i].Err = errors.New(rec.Error)
		default:
			results[i].Sum = rec.Hash
		}
		n++
	}
	if err := sc.Err(); err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	waitErr := cmd.Wait()
	// Exit status 1 means "some file failed" and is reflected per record;
	// anything else, or missing output, is a helper failure.
	var exitErr *exec.ExitError
	if waitErr != nil && !(errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1) {
		return nil, fmt.Errorf("hash helper failed: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if n != len(sendIdx) {
		return nil, fmt.Errorf("hash helper returned %d results for %d paths", n, len(sendIdx))
	}
	return results, nil
}
