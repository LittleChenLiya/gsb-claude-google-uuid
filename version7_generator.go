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

// ErrV7SequenceExhausted is returned by V7Generator.Generate when the 12-bit
// sequence counter (rand_a) for the current millisecond has been exhausted.
//
// The generator rejects the request instead of busy-waiting for the next
// millisecond. The internal state is left unchanged, so a later call with an
// advancing clock (or after the clock moves forward) can still succeed.
var ErrV7SequenceExhausted = errors.New("uuid: v7 sequence exhausted for current millisecond")

// V7Generator is an independent generator of UUID version 7 values.
//
// Every V7Generator owns its own clock, random source and sequence state; it
// does not share state with the package-level NewV7 function or with any other
// V7Generator. A generator is safe for concurrent use by multiple goroutines:
// all calls are serialized within the instance.
//
// Monotonicity and ordering guarantees are per instance only. The generator
// guarantees that successive values returned by the same instance are strictly
// increasing when compared as raw bytes, but it makes no claim about ordering
// across instances, processes or machines.
//
// State rollback: the generator reads all required random bytes before
// committing any internal state. If the random source fails (for example a
// short read), the call returns an error and the generator state is rolled
// back unchanged; the next call continues as if the failed call had never
// happened.
type V7Generator struct {
	mu          sync.Mutex
	now         func() int64 // milliseconds since the Unix epoch
	r           io.Reader
	initialized bool
	lastMilli   int64
	lastSeq     uint16 // 12-bit rand_a sequence for lastMilli
}

// V7Option configures a V7Generator.
type V7Option func(*V7Generator)

// NewV7Generator returns a new independent UUID version 7 generator.
//
// By default the generator uses the system wall clock (time.Now, Unix
// milliseconds) and the crypto/rand reader. Use WithClock and WithReader to
// inject a controllable millisecond clock and random source.
func NewV7Generator(opts ...V7Option) *V7Generator {
	g := &V7Generator{
		now: func() int64 { return time.Now().UnixNano() / int64(time.Millisecond) },
		r:   rand.Reader,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// WithClock overrides the millisecond clock used by the generator. now must
// return the number of milliseconds since the Unix epoch.
func WithClock(now func() int64) V7Option {
	return func(g *V7Generator) {
		if now != nil {
			g.now = now
		}
	}
}

// WithReader overrides the random source used by the generator. r must return
// random bytes; a failing or short read is reported by Generate and leaves the
// generator state unchanged.
func WithReader(r io.Reader) V7Option {
	return func(g *V7Generator) {
		if r != nil {
			g.r = r
		}
	}
}

// Generate returns a new version 7 UUID from this generator.
//
// The returned UUID is strictly greater, when compared as raw bytes, than every
// UUID previously returned by the same instance. If the injected clock reports
// a time earlier than the last observed millisecond, the generator keeps the
// last millisecond and advances its sequence instead of going backwards.
//
// If the 12-bit sequence for the current millisecond is exhausted, Generate
// returns ErrV7SequenceExhausted without busy-waiting and without modifying
// state. If the random source fails, Generate returns the read error and rolls
// back any state change.
func (g *V7Generator) Generate() (UUID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	milli := g.now()

	var (
		ts      int64
		seq     uint16
		advance bool
	)

	if !g.initialized || milli > g.lastMilli {
		// New millisecond (or first call): the timestamp advances and the
		// sequence is seeded from the random source below.
		ts = milli
		advance = true
	} else {
		// Same millisecond, or the clock moved backwards: keep the last
		// timestamp and continue the sequence.
		ts = g.lastMilli
		seq = g.lastSeq + 1
		if seq > 0x0fff {
			// Sequence exhausted: reject instead of busy-waiting. State is
			// intentionally not modified.
			return Nil, ErrV7SequenceExhausted
		}
	}

	// Read all random bytes before committing state so that a random-source
	// failure rolls back cleanly.
	var b [10]byte
	if _, err := io.ReadFull(g.r, b[:]); err != nil {
		return Nil, err
	}

	if advance {
		// rand_a: 12 bits of randomness for a fresh timestamp.
		seq = uint16(b[0])<<4 | uint16(b[1]>>4)
	}

	// Commit state only after every operation that can fail has succeeded.
	g.initialized = true
	g.lastMilli = ts
	g.lastSeq = seq

	var u UUID
	u[0] = byte(ts >> 40)
	u[1] = byte(ts >> 32)
	u[2] = byte(ts >> 24)
	u[3] = byte(ts >> 16)
	u[4] = byte(ts >> 8)
	u[5] = byte(ts)

	u[6] = 0x70 | byte(seq>>8) // version 7, high nibble of rand_a
	u[7] = byte(seq)           // low 8 bits of rand_a

	u[8] = (b[2] & 0x3f) | 0x80 // variant 10, high 6 bits of rand_b
	u[9] = b[3]
	u[10] = b[4]
	u[11] = b[5]
	u[12] = b[6]
	u[13] = b[7]
	u[14] = b[8]
	u[15] = b[9]

	return u, nil
}
