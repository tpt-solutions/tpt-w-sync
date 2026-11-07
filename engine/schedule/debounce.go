// SPDX-License-Identifier: MIT OR Apache-2.0
// Copyright (c) 2026 TPT Solutions

package schedule

import (
	"sync"
	"time"
)

// Debouncer coalesces bursts of Trigger calls into one call to fire, made once
// no Trigger has arrived for delay. If maxWait > 0, fire is forced no later than
// maxWait after the first Trigger of a burst, so continuous churn cannot starve
// the sync forever.
type Debouncer struct {
	delay   time.Duration
	maxWait time.Duration
	fire    func()

	mu    sync.Mutex
	timer *time.Timer
	gen   uint64
	first time.Time
}

// NewDebouncer returns a Debouncer that calls fire after quiet periods.
func NewDebouncer(delay, maxWait time.Duration, fire func()) *Debouncer {
	return &Debouncer{delay: delay, maxWait: maxWait, fire: fire}
}

// Trigger records a change event, (re)starting the quiet-period timer.
func (d *Debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if d.timer == nil {
		d.first = now
	} else {
		d.timer.Stop()
	}
	wait := d.delay
	if d.maxWait > 0 {
		remaining := d.maxWait - now.Sub(d.first)
		if remaining < wait {
			wait = max(remaining, 0)
		}
	}
	d.gen++
	gen := d.gen
	d.timer = time.AfterFunc(wait, func() { d.expire(gen) })
}

func (d *Debouncer) expire(gen uint64) {
	d.mu.Lock()
	if gen != d.gen || d.timer == nil {
		// A newer Trigger superseded this timer.
		d.mu.Unlock()
		return
	}
	d.timer = nil
	d.mu.Unlock()
	d.fire()
}

// Stop cancels any pending fire.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.gen++
}
