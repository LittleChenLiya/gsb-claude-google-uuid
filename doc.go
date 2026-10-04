// Copyright 2016 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package uuid generates and inspects UUIDs.
//
// UUIDs are based on RFC 9562(obsoletes RFC 4122) and DCE 1.1: Authentication and Security
// Services.
//
// A UUID is a 16 byte (128 bit) array.  UUIDs may be used as keys to
// maps or compared directly.
//
// # Monotonic version 7 generator
//
// NewV7Generator returns an independent V7Generator with injectable
// millisecond clock and random source. UUIDs produced by one instance are
// strictly increasing by raw byte value, including across backwards clock
// movement (the per-millisecond 12 bit sequence continues from the previous
// millisecond). Sequence exhaustion returns ErrV7SequenceExhausted and never
// busy-waits or repeats a value; a failed random read commits no internal
// state, so the following call can still succeed.
//
// These guarantees are scoped to a single V7Generator instance: they do not
// extend across separate instances, processes or machines, and the package
// level NewV7 function keeps its own independent behavior and state.
package uuid
