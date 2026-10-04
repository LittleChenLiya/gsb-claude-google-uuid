// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"crypto/rand"
	"errors"
	"io"
	"sync"
	"time"
)

// v7SeqMax is the full 12 bit rand_a/seq range allowed by RFC 9562 for
// UUID version 7. It is the default internal sequence boundary of
// V7Generator; in-package tests may lower it to exercise deterministic
// sequence exhaustion.
const v7SeqMax = 0xFFF

// ErrV7SequenceExhausted is returned by V7Generator.New when the 12 bit
// per-millisecond monotonic sequence is exhausted for the current
// millisecond.
//
// The generator never busy-waits, sleeps, or fabricates a duplicate in
// this situation: it rejects the request immediately and leaves its
// internal state untouched. A later call that observes a clock value
// strictly greater than the last committed millisecond succeeds again.
var ErrV7SequenceExhausted = errors.New("uuid: v7 generator sequence exhausted for current millisecond")

// V7ClockFunc returns the current time as Unix epoch milliseconds. It is
// the injectable millisecond clock used by V7Generator.
type V7ClockFunc func() int64

// V7Config configures a V7Generator created with NewV7Generator.
//
// When Clock is nil the wall clock (time.Now) is used. When Rand is nil
// the package default random source (crypto/rand.Reader) is used.
type V7Config struct {
	// Clock supplies the current Unix epoch time in milliseconds.
	Clock V7ClockFunc

	// Rand supplies the random bytes. Exactly 16 bytes are requested per
	// UUID via io.ReadFull, so a reader that returns fewer bytes causes the
	// call to fail without modifying the generator state.
	Rand io.Reader
}

// V7Generator is an independent UUID version 7 generator instance with
// its own monotonic state, injectable millisecond clock and injectable
// random source.
//
// UUIDs returned by a single V7Generator are strictly increasing when
// compared as 16 byte values (and therefore also when compared as
// canonical strings). The guarantee holds even if the injected clock
// moves backwards: on a non-advancing or backwards clock the 12 bit
// sequence field (rand_a) is incremented, continuing from the sequence
// of the previous millisecond, while the 62 random bits (rand_b) still
// come from the configured random source.
//
// If the sequence cannot be incremented without exceeding its 12 bit
// range, New returns ErrV7SequenceExhausted immediately. It does not
// busy-wait or sleep and it never returns a duplicate UUID.
//
// If reading from the random source fails (including a short read),
// New returns that error and does not commit any internal state: the
// last millisecond and sequence are unchanged, so a subsequent call can
// succeed as normal.
//
// All New calls on one instance are serialized by an internal mutex.
//
// Scope of the guarantees:
//
//   - Strict ordering is provided only within a single V7Generator
//     instance. It is NOT claimed across different instances, across
//     processes or machines, or relative to the package level NewV7
//     function. Each generator owns independent state.
//   - The package level NewV7 and NewV7FromReader functions are
//     unchanged and keep their own behavior and separate global state.
type V7Generator struct {
	mu     sync.Mutex
	clock  V7ClockFunc
	rander io.Reader

	// started reports whether the first UUID has been committed.
	started bool
	// lastMilli is the millisecond of the last committed UUID.
	lastMilli int64
	// lastSeq is the 12 bit sequence of the last committed UUID.
	lastSeq uint16
	// seqMax is the internal upper bound of the sequence. It defaults to
	// v7SeqMax (0xFFF) and can only be lowered by in-package tests to
	// exercise deterministic sequence exhaustion.
	seqMax uint16
}

// NewV7Generator creates an independent UUID version 7 generator.
//
// A nil config, or a config with nil Clock/Rand fields, selects the
// wall clock and crypto/rand.Reader respectively.
func NewV7Generator(cfg *V7Config) *V7Generator {
	g := &V7Generator{
		clock: func() int64 {
			return time.Now().UnixMilli()
		},
		rander: rand.Reader,
		seqMax: v7SeqMax,
	}
	if cfg != nil {
		if cfg.Clock != nil {
			g.clock = cfg.Clock
		}
		if cfg.Rand != nil {
			g.rander = cfg.Rand
		}
	}
	return g
}

// New returns a strictly increasing, RFC 9562 valid version 7 UUID.
//
// It returns ErrV7SequenceExhausted when the per-millisecond 12 bit
// sequence is exhausted, without busy-waiting. It returns the random
// source error (and leaves state untouched) when random bytes cannot be
// read.
func (g *V7Generator) New() (UUID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	milli := g.clock()

	// freshMilli means the next UUID starts a new millisecond whose
	// initial sequence is taken from randomness. Otherwise the sequence
	// continues the previous millisecond by one.
	freshMilli := !g.started || milli > g.lastMilli
	var seq uint16
	if !freshMilli {
		// Same millisecond, or a clock that stood still / moved backwards:
		// keep emitting on the last committed millisecond and advance the
		// sequence by one so the byte value stays strictly larger.
		if g.lastSeq >= g.seqMax {
			// Reject immediately; no busy-waiting and no duplicate.
			// State is intentionally not modified.
			return Nil, ErrV7SequenceExhausted
		}
		milli = g.lastMilli
		seq = g.lastSeq + 1
	}

	var uuid UUID
	// Request all randomness before committing state. io.ReadFull turns a
	// short read into an error; on any failure started/lastMilli/lastSeq
	// stay unchanged, so the next call behaves as if this one never ran.
	if _, err := io.ReadFull(g.rander, uuid[:]); err != nil {
		return Nil, err
	}

	if freshMilli {
		// Low 12 bits of rand_a seed the new millisecond's sequence.
		seq = uint16(uuid[6]&0x0F)<<8 | uint16(uuid[7])
		if seq > g.seqMax {
			seq = g.seqMax
		}
	}

	uuid[0] = byte(milli >> 40)
	uuid[1] = byte(milli >> 32)
	uuid[2] = byte(milli >> 24)
	uuid[3] = byte(milli >> 16)
	uuid[4] = byte(milli >> 8)
	uuid[5] = byte(milli)
	uuid[6] = 0x70 | byte(seq>>8)     // version 7 + high 4 bits of seq
	uuid[7] = byte(seq)               // low 8 bits of seq
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // RFC 4122 variant

	// Commit internal state only after a fully successful generation.
	g.started = true
	g.lastMilli = milli
	g.lastSeq = seq

	return uuid, nil
}
