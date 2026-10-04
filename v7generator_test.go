// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

// scriptedClock returns a fixed sequence of millisecond values, then the
// last value for any further calls.
type scriptedClock struct {
	ticks []int64
	pos   int
}

func (c *scriptedClock) now() int64 {
	i := c.pos
	if i >= len(c.ticks) {
		i = len(c.ticks) - 1
	}
	c.pos++
	return c.ticks[i]
}

// infiniteZeroReader returns as many zero bytes as requested, forever.
type infiniteZeroReader struct{}

func (infiniteZeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// shortReader returns 3 bytes followed by io.EOF, so io.ReadFull fails
// with io.ErrUnexpectedEOF: a full 16 byte UUID can never be built.
type shortReader struct{}

func (shortReader) Read(p []byte) (int, error) {
	n := copy(p, []byte{0x01, 0x02, 0x03})
	return n, io.EOF
}

// v7MilliSeq decodes the 48 bit unix_ts_ms and 12 bit seq fields.
func v7MilliSeq(u UUID) (int64, uint16) {
	milli := int64(u[0])<<40 | int64(u[1])<<32 | int64(u[2])<<24 |
		int64(u[3])<<16 | int64(u[4])<<8 | int64(u[5])
	seq := uint16(u[6]&0x0F)<<8 | uint16(u[7])
	return milli, seq
}

// TestV7GeneratorClockRollback is the primary acceptance case. The clock
// reports 1000, 1000, 999 (a backwards jump), 1001 and the four UUIDs must
// all be valid version 7 values and strictly increasing by raw bytes.
func TestV7GeneratorClockRollback(t *testing.T) {
	clk := &scriptedClock{ticks: []int64{1000, 1000, 999, 1001}}
	g := NewV7Generator(&V7Config{
		Clock: clk.now,
		Rand:  infiniteZeroReader{},
	})

	var got [4]UUID
	for i := range got {
		u, err := g.New()
		if err != nil {
			t.Fatalf("New #%d returned error: %v", i, err)
		}
		if u.Version() != 7 {
			t.Fatalf("New #%d has version %d, want 7", i, u.Version())
		}
		if u.Variant() != RFC4122 {
			t.Fatalf("New #%d has variant %v, want RFC4122", i, u.Variant())
		}
		got[i] = u
	}

	// Expected (milli, seq): the first call seeds seq=0 from the zero
	// reader, the duplicate/backwards clocks continue seq 1 then 2 on
	// millisecond 1000, and 1001 reseeds seq=0.
	want := []struct {
		milli int64
		seq   uint16
	}{
		{1000, 0},
		{1000, 1},
		{1000, 2},
		{1001, 0},
	}
	for i, w := range want {
		milli, seq := v7MilliSeq(got[i])
		if milli != w.milli || seq != w.seq {
			t.Errorf("UUID #%d = (milli %d, seq %d), want (milli %d, seq %d)",
				i, milli, seq, w.milli, w.seq)
		}
	}

	// Strict byte ordering across the whole sequence.
	for i := 1; i < len(got); i++ {
		if bytes.Compare(got[i-1][:], got[i][:]) >= 0 {
			t.Fatalf("UUID #%d (%s) is not strictly greater than #%d (%s)",
				i, got[i], i-1, got[i-1])
		}
	}

	// The clock was called exactly once per generated UUID.
	if clk.pos != 4 {
		t.Errorf("clock called %d times, want 4", clk.pos)
	}
}

// TestV7GeneratorRandomFailureNoStateCommit verifies that a short read
// from the random source fails the call without advancing any internal
// counter state: after the failure the next UUID is exactly the value
// that would have been generated without the failed call.
func TestV7GeneratorRandomFailureNoStateCommit(t *testing.T) {
	clk := &scriptedClock{ticks: []int64{1000, 1000, 1000}}
	g := NewV7Generator(&V7Config{Clock: clk.now, Rand: infiniteZeroReader{}})

	before, err := g.New()
	if err != nil {
		t.Fatalf("seed New: %v", err)
	}

	// Swap in a reader that can never supply 16 bytes.
	g.rander = shortReader{}
	failed, err := g.New()
	if err == nil {
		t.Fatalf("New with short reader unexpectedly succeeded: %s", failed)
	}
	if failed != Nil {
		t.Errorf("failed New returned %s, want Nil", failed)
	}

	// State must not have advanced: started stays true, and the committed
	// (milli, seq) are unchanged.
	g.mu.Lock()
	milli, seq, started := g.lastMilli, g.lastSeq, g.started
	g.mu.Unlock()
	if !started {
		t.Errorf("started flag changed after random failure")
	}
	bm, bs := v7MilliSeq(before)
	if milli != bm || seq != bs {
		t.Errorf("state advanced after failure: got (%d,%d), want (%d,%d)",
			milli, seq, bm, bs)
	}

	// Restore a healthy source: generation continues normally and the
	// next UUID follows the one produced before the failure.
	g.rander = infiniteZeroReader{}
	after, err := g.New()
	if err != nil {
		t.Fatalf("New after recovery: %v", err)
	}
	if bytes.Compare(before[:], after[:]) >= 0 {
		t.Fatalf("UUID after failure (%s) not greater than before (%s)", after, before)
	}
	am, aseq := v7MilliSeq(after)
	if am != bm || aseq != bs+1 {
		t.Errorf("recovered UUID = (%d,%d), want (%d,%d)", am, aseq, bm, bs+1)
	}
}

// TestV7GeneratorSequenceExhausted uses the controllable internal
// sequence boundary to prove that exhaustion rejects immediately instead
// of busy-waiting or returning a duplicate.
func TestV7GeneratorSequenceExhausted(t *testing.T) {
	g := NewV7Generator(&V7Config{
		Clock: func() int64 { return 1000 }, // clock never advances
		Rand:  infiniteZeroReader{},
	})
	g.seqMax = 1 // controllable internal boundary

	// seq 0
	first, err := g.New()
	if err != nil {
		t.Fatalf("New first: %v", err)
	}
	m0, s0 := v7MilliSeq(first)
	if m0 != 1000 || s0 != 0 {
		t.Fatalf("first UUID = (%d,%d), want (1000,0)", m0, s0)
	}

	// seq 1 (the last value allowed by the lowered boundary)
	second, err := g.New()
	if err != nil {
		t.Fatalf("New second: %v", err)
	}
	m1, s1 := v7MilliSeq(second)
	if m1 != 1000 || s1 != 1 {
		t.Fatalf("second UUID = (%d,%d), want (1000,1)", m1, s1)
	}

	// Next call must be rejected, not spin, sleep or duplicate.
	u, err := g.New()
	if !errors.Is(err, ErrV7SequenceExhausted) {
		t.Fatalf("exhausted New returned err=%v, want ErrV7SequenceExhausted", err)
	}
	if u != Nil {
		t.Errorf("exhausted New returned %s, want Nil", u)
	}

	// State is unchanged by the rejected call.
	g.mu.Lock()
	milli, seq := g.lastMilli, g.lastSeq
	g.mu.Unlock()
	if milli != 1000 || seq != 1 {
		t.Errorf("state changed after exhaustion: got (%d,%d), want (1000,1)", milli, seq)
	}

	// Once the clock moves past the stuck millisecond generation resumes.
	g.clock = func() int64 { return 1001 }
	third, err := g.New()
	if err != nil {
		t.Fatalf("New after clock advance: %v", err)
	}
	if bytes.Compare(second[:], third[:]) >= 0 {
		t.Fatalf("UUID after recovery (%s) not greater than %s", third, second)
	}
}

// TestV7GeneratorConcurrentUnique hammers one instance from many goroutines
// and verifies every UUID is unique, version 7, and serialized through the
// instance mutex.
func TestV7GeneratorConcurrentUnique(t *testing.T) {
	g := NewV7Generator(&V7Config{
		Clock: func() int64 { return 5000 },
		Rand:  infiniteZeroReader{},
	})

	const goroutines = 16
	const perG = 128
	var wg sync.WaitGroup
	results := make([][]UUID, goroutines)
	errCh := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			out := make([]UUID, 0, perG)
			for j := 0; j < perG; j++ {
				u, err := g.New()
				if err != nil {
					errCh <- err
					return
				}
				out = append(out, u)
			}
			results[i] = out
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent New failed: %v", err)
	}

	total := goroutines * perG // 2048 < 4096, so seq never exhausts
	seen := make(map[UUID]struct{}, total)
	for _, batch := range results {
		for _, u := range batch {
			if u.Version() != 7 {
				t.Fatalf("UUID %s has version %d, want 7", u, u.Version())
			}
			if u.Variant() != RFC4122 {
				t.Fatalf("UUID %s has variant %v, want RFC4122", u, u.Variant())
			}
			if _, dup := seen[u]; dup {
				t.Fatalf("duplicate UUID %s generated under concurrency", u)
			}
			seen[u] = struct{}{}
		}
	}
	if len(seen) != total {
		t.Fatalf("generated %d unique UUIDs, want %d", len(seen), total)
	}

	// Serialization means exactly one committed increment per UUID, all on
	// the single millisecond: seq runs 0..total-1 with no gaps.
	g.mu.Lock()
	milli, seq := g.lastMilli, g.lastSeq
	g.mu.Unlock()
	if milli != 5000 || int(seq) != total-1 {
		t.Fatalf("final state = (%d,%d), want (5000,%d)", milli, seq, total-1)
	}
}

// TestV7GeneratorDefaults checks that a nil config yields a usable
// generator that still produces increasing version 7 UUIDs.
func TestV7GeneratorDefaults(t *testing.T) {
	g := NewV7Generator(nil)
	u1, err := g.New()
	if err != nil {
		t.Fatalf("default New first: %v", err)
	}
	u2, err := g.New()
	if err != nil {
		t.Fatalf("default New second: %v", err)
	}
	if u1.Version() != 7 || u2.Version() != 7 {
		t.Fatalf("default generator produced non-v7 UUIDs")
	}
	if u1.Variant() != RFC4122 || u2.Variant() != RFC4122 {
		t.Fatalf("default generator produced non-RFC4122 UUIDs")
	}
	if bytes.Compare(u1[:], u2[:]) >= 0 {
		t.Fatalf("default UUIDs not increasing: %s !< %s", u1, u2)
	}
}
