// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package engine

import (
	"context"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"golang.org/x/time/rate"
)

// minBurst keeps small limits usable: a read never waits for more tokens than
// the bucket can hold.
const minBurst = 64 << 10

// newLimiter returns a limiter for bytesPerSecond, shared by all transfers in
// one run. It returns nil when there is no limit.
func newLimiter(bytesPerSecond int64) *rate.Limiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	burst := int(bytesPerSecond / 10)
	if burst < minBurst {
		burst = minBurst
	}
	return rate.NewLimiter(rate.Limit(bytesPerSecond), burst)
}

// throttledReader paces reads from r through lim.
type throttledReader struct {
	ctx context.Context
	r   io.Reader
	lim *rate.Limiter
}

// throttle wraps r so reads consume the limiter's budget. A nil limiter returns r.
func throttle(ctx context.Context, r io.Reader, lim *rate.Limiter) io.Reader {
	if lim == nil {
		return r
	}
	return &throttledReader{ctx: ctx, r: r, lim: lim}
}

func (t *throttledReader) Read(p []byte) (int, error) {
	// Never ask for more tokens than the bucket holds in one go.
	if max := t.lim.Burst(); len(p) > max {
		p = p[:max]
	}
	n, err := t.r.Read(p)
	if n > 0 {
		if werr := t.lim.WaitN(t.ctx, n); werr != nil {
			return n, werr
		}
	}
	return n, err
}

// ParseBytes parses a size such as "1500", "64K", "10M" or "2G" (binary units).
// An empty string is zero.
func ParseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, nil
	}
	s = strings.TrimSuffix(s, "B") // "10MB" and "10M" are the same
	mult := int64(1)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K':
			mult, s = 1<<10, s[:n-1]
		case 'M':
			mult, s = 1<<20, s[:n-1]
		case 'G':
			mult, s = 1<<30, s[:n-1]
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 || n > math.MaxInt64/mult {
		return 0, fmt.Errorf("bad size %q (use bytes or a K/M/G suffix)", s)
	}
	return n * mult, nil
}
