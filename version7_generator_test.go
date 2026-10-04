// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"errors"
	"io"
	"sync"
	"testing"
)

// fillReader fills every requested byte with a fixed pattern.
type fillReader byte

func (r fillReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// scriptedReader returns random bytes until fail is set, then fails (or
// short-reads) so tests can drive random-source failures deterministically.
type scriptedReader struct {
	mu      sync.Mutex
	fail    bool
	short   bool
	reads   int
	fill    byte
	lastErr error
}

func (r *scriptedReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if r.fail {
		if r.short {
			n := len(p) - 1
			if n < 0 {
				n = 0
			}
			return n, io.ErrUnexpectedEOF
		}
		r.lastErr = errors.New("random source unavailable")
		return 0, r.lastErr
	}
	for i := range p {
		p[i] = r.fill
	}
	return len(p), nil
}

func (r *scriptedReader) setFail(fail bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = fail
}

func (r *scriptedReader) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

// TestV7Generator_MonotonicClock drives the clock 1000, 1000, 999, 1001 and
// expects four valid version 7 UUIDs that are strictly increasing when
// compared as raw bytes. The 999 reading is a clock rollback: the generator
// must keep the last millisecond (1000) and advance its sequence.
func TestV7Generator_MonotonicClock(t *testing.T) {
	millis := []int64{1000, 1000, 999, 1001}
	idx := 0
	g := NewV7Generator(
		WithClock(func() int64 {
			m := millis[idx]
			idx++
			return m
		}),
		WithReader(fillReader(0xAB)),
	)

	us := make([]UUID, 4)
	for i := range us {
		u, err := g.Generate()
		if err != nil {
			t.Fatalf("Generate() call %d: unexpected error: %v", i, err)
		}
		us[i] = u
	}

	for i, u := range us {
		if u.Version() != 7 {
			t.Errorf("uuid %d: version = %d, want 7", i, u.Version())
		}
		if u.Variant() != RFC4122 {
			t.Errorf("uuid %d: variant = %v, want RFC4122", i, u.Variant())
		}
	}

	// rand_a is seeded to 0xABC for a fresh millisecond and increments for
	// the two same-millisecond/rollback calls.
	wantSeq := []byte{0xBA, 0xBB, 0xBC, 0xBA}
	for i, u := range us {
		if u[7] != wantSeq[i] {
			t.Errorf("uuid %d: rand_a low byte = 0x%02x, want 0x%02x", i, u[7], wantSeq[i])
		}
		if u[6] != 0x7A {
			t.Errorf("uuid %d: byte 6 = 0x%02x, want 0x7A (version 7 | rand_a high nibble)", i, u[6])
		}
	}

	// The 999 rollback keeps timestamp 1000; the 1001 call advances again.
	wantMilli := []int64{1000, 1000, 1000, 1001}
	for i, u := range us {
		if got := int64((u.Time() - g1582ns100) / 10000); got != wantMilli[i] {
			t.Errorf("uuid %d: unix millis = %d, want %d", i, got, wantMilli[i])
		}
	}

	for i := 1; i < len(us); i++ {
		if Compare(us[i-1], us[i]) >= 0 {
			t.Errorf("uuid %d (%s) is not greater than uuid %d (%s)",
				i, us[i], i-1, us[i-1])
		}
	}
}

// TestV7Generator_RandomFailureRollback injects a failing (and a short-read)
// random source and verifies that a failed Generate does not advance the
// internal sequence state: the next successful call uses the sequence number
// the failed call would have consumed.
func TestV7Generator_RandomFailureRollback(t *testing.T) {
	r := &scriptedReader{fill: 0x42}
	g := NewV7Generator(
		WithClock(func() int64 { return 1000 }),
		WithReader(r),
	)

	u1, err := g.Generate()
	if err != nil {
		t.Fatalf("initial Generate: %v", err)
	}
	if got := u1[7]; got != 0x24 { // rand_a 0x424
		t.Fatalf("initial rand_a low byte = 0x%02x, want 0x24", got)
	}

	// Failing source: state must not change.
	r.setFail(true)
	if _, err := g.Generate(); err == nil {
		t.Fatal("Generate with failing reader: expected error, got nil")
	}
	if g.lastSeq != 0x424 {
		t.Errorf("after failure: lastSeq = 0x%03x, want 0x424 (unchanged)", g.lastSeq)
	}
	if g.lastMilli != 1000 {
		t.Errorf("after failure: lastMilli = %d, want 1000 (unchanged)", g.lastMilli)
	}
	if !g.initialized {
		t.Error("after failure: generator marked uninitialized")
	}

	// Short-read source: io.ReadFull must report ErrUnexpectedEOF and state
	// must still not change.
	r.short = true
	if _, err := g.Generate(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Generate with short reader: error = %v, want io.ErrUnexpectedEOF", err)
	}
	if g.lastSeq != 0x424 {
		t.Errorf("after short read: lastSeq = 0x%03x, want 0x424 (unchanged)", g.lastSeq)
	}

	// Recovery: the next call consumes the next sequence number (0x425),
	// proving the failed calls did not consume a sequence.
	r.setFail(false)
	u2, err := g.Generate()
	if err != nil {
		t.Fatalf("recovery Generate: %v", err)
	}
	if got := u2[7]; got != 0x25 { // rand_a 0x425
		t.Errorf("recovery rand_a low byte = 0x%02x, want 0x25", got)
	}
	if Compare(u1, u2) >= 0 {
		t.Errorf("recovery uuid %s is not greater than %s", u2, u1)
	}
}

// TestV7Generator_SequenceExhaustion drives the 12-bit sequence to its
// controllable internal boundary (0x0fff) and verifies that the generator
// rejects the next same-millisecond call with ErrV7SequenceExhausted instead
// of busy-waiting, leaves state unchanged, and succeeds again once the clock
// advances.
func TestV7Generator_SequenceExhaustion(t *testing.T) {
	r := &scriptedReader{fill: 0x77}
	var clock int64 = 1000
	g := NewV7Generator(
		WithClock(func() int64 { return clock }),
		WithReader(r),
	)

	// Seed the internal state directly at the sequence boundary.
	g.initialized = true
	g.lastMilli = 1000
	g.lastSeq = 0x0fff

	readsBefore := r.readCount()
	if _, err := g.Generate(); !errors.Is(err, ErrV7SequenceExhausted) {
		t.Fatalf("Generate at exhausted sequence: error = %v, want ErrV7SequenceExhausted", err)
	}
	if got := r.readCount(); got != readsBefore {
		t.Errorf("exhausted call performed %d reads, want 0", got-readsBefore)
	}
	if g.lastSeq != 0x0fff || g.lastMilli != 1000 {
		t.Errorf("after exhaustion: lastSeq=0x%03x lastMilli=%d, want 0xfff/1000 (unchanged)",
			g.lastSeq, g.lastMilli)
	}

	// Still the same millisecond: rejection is deterministic, no busy-wait.
	if _, err := g.Generate(); !errors.Is(err, ErrV7SequenceExhausted) {
		t.Fatalf("second exhausted Generate: error = %v, want ErrV7SequenceExhausted", err)
	}

	// Clock advances: the generator re-seeds rand_a and succeeds.
	clock = 1001
	u, err := g.Generate()
	if err != nil {
		t.Fatalf("Generate after clock advance: %v", err)
	}
	if g.lastMilli != 1001 {
		t.Errorf("after advance: lastMilli = %d, want 1001", g.lastMilli)
	}
	if int64((u.Time()-g1582ns100)/10000) != 1001 {
		t.Errorf("uuid millis = %d, want 1001", (u.Time()-g1582ns100)/10000)
	}
	if u.Version() != 7 || u.Variant() != RFC4122 {
		t.Errorf("uuid version=%d variant=%v, want 7/RFC4122", u.Version(), u.Variant())
	}
}

// TestV7Generator_Concurrent hammers a single generator from many goroutines
// and verifies that every UUID is unique, version 7, RFC4122 variant, and
// that the serialized calls produce globally byte-increasing values.
func TestV7Generator_Concurrent(t *testing.T) {
	var clock int64
	g := NewV7Generator(
		WithClock(func() int64 {
			clock++
			return clock
		}),
	)

	const goroutines = 32
	const perGoroutine = 100
	results := make(chan UUID, goroutines*perGoroutine)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				u, err := g.Generate()
				if err != nil {
					t.Errorf("concurrent Generate: %v", err)
					return
				}
				results <- u
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[UUID]bool, goroutines*perGoroutine)
	for u := range results {
		if u.Version() != 7 {
			t.Errorf("version = %d, want 7", u.Version())
		}
		if u.Variant() != RFC4122 {
			t.Errorf("variant = %v, want RFC4122", u.Variant())
		}
		if seen[u] {
			t.Errorf("duplicate UUID generated: %s", u)
		}
		seen[u] = true
	}
	if len(seen) != goroutines*perGoroutine {
		t.Errorf("got %d unique UUIDs, want %d", len(seen), goroutines*perGoroutine)
	}

	// Re-drive the generator deterministically to confirm strict byte-order
	// across serialized calls.
	g2 := NewV7Generator(
		WithClock(func() int64 { return 2000 }),
		WithReader(fillReader(0x01)),
	)
	var last UUID
	for i := 0; i < 10; i++ {
		u, err := g2.Generate()
		if err != nil {
			t.Fatalf("deterministic Generate %d: %v", i, err)
		}
		if i > 0 && Compare(last, u) >= 0 {
			t.Fatalf("deterministic pair %s -> %s not increasing", last, u)
		}
		last = u
	}
}
