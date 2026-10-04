// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"errors"
	"strings"
	"testing"
)

// TestRegistrarTwoNamespacesSameInput verifies that two namespaces with the
// same input string derive different UUIDs (same bytes, different namespace).
func TestRegistrarTwoNamespacesSameInput(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns1 := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	ns2 := MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns1", ns1); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("ns2", ns2); err != nil {
		t.Fatal(err)
	}
	input := "example.com"
	u1, err := r.Derive("ns1", input, DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := r.Derive("ns2", input, DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	if u1 == u2 {
		t.Fatalf("expected different UUIDs for different namespaces, got %s and %s", u1, u2)
	}
}

// TestRegistrarSameNamespaceRepeatedInput verifies that the same namespace
// and the same input produce a consistent UUID on repeated calls.
func TestRegistrarSameNamespaceRepeatedInput(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns", ns); err != nil {
		t.Fatal(err)
	}
	input := "example.com"
	u1, err := r.Derive("ns", input, DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := r.Derive("ns", input, DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	if u1 != u2 {
		t.Fatalf("expected consistent UUID, got %s and %s", u1, u2)
	}
	v3a, err := r.Derive("ns", input, DeriveV3)
	if err != nil {
		t.Fatal(err)
	}
	v3b, err := r.Derive("ns", input, DeriveV3)
	if err != nil {
		t.Fatal(err)
	}
	if v3a != v3b {
		t.Fatalf("expected consistent v3 UUID, got %s and %s", v3a, v3b)
	}
}

// TestRegistrarNameConflict verifies that registering the same name with a
// different UUID returns a NameConflictError and does not modify the
// registrar.
func TestRegistrarNameConflict(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns1 := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	ns2 := MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns", ns1); err != nil {
		t.Fatal(err)
	}
	err := r.Register("ns", ns2)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	var conflict *NameConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected NameConflictError, got %T: %v", err, err)
	}
	if conflict.Name == (NameHash{}) {
		t.Fatal("expected non-empty name hash in conflict report")
	}
	u, err := r.Derive("ns", "x", DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	expected := NewSHA1(ns1, []byte("x"))
	if u != expected {
		t.Fatalf("registrar was modified after conflict: got %s, want %s", u, expected)
	}
}

// TestRegistrarAliasRing verifies that adding an alias that closes a loop
// returns an AliasRingError whose Chain field reports the ring.
func TestRegistrarAliasRing(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("a", ns); err != nil {
		t.Fatal(err)
	}
	if err := r.AddAlias("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddAlias("c", "a"); err != nil {
		t.Fatal(err)
	}
	// Change b -> c: now b -> c -> a (still no ring).
	if err := r.AddAlias("b", "c"); err != nil {
		t.Fatal(err)
	}
	// Change c -> b: this closes the ring c -> b -> c.
	err := r.AddAlias("c", "b")
	if err == nil {
		t.Fatal("expected alias ring error, got nil")
	}
	var ring *AliasRingError
	if !errors.As(err, &ring) {
		t.Fatalf("expected AliasRingError, got %T: %v", err, err)
	}
	if len(ring.Chain) < 2 {
		t.Fatalf("expected chain with at least 2 entries, got %d", len(ring.Chain))
	}
	msg := ring.Error()
	if !strings.Contains(msg, "->") {
		t.Fatalf("expected chain arrows in error message, got %q", msg)
	}
	// The report must not store raw name content as standalone tokens.
	for _, token := range strings.Fields(msg) {
		if token == "a" || token == "b" || token == "c" {
			t.Fatalf("raw name content leaked in report: %q", msg)
		}
	}
}

// TestRegistrarSelfLoop verifies that a self-referencing alias is rejected.
func TestRegistrarSelfLoop(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("a", ns); err != nil {
		t.Fatal(err)
	}
	if err := r.AddAlias("b", "a"); err != nil {
		t.Fatal(err)
	}
	// Change b -> b: this creates a self-loop.
	err := r.AddAlias("b", "b")
	if err == nil {
		t.Fatal("expected self-loop rejection, got nil")
	}
	var ring *AliasRingError
	if !errors.As(err, &ring) {
		t.Fatalf("expected AliasRingError, got %T: %v", err, err)
	}
}

// TestRegistrarSnapshotImmutable verifies that a snapshot is an immutable
// point-in-time view and that later changes to the registrar do not affect
// the snapshot.
func TestRegistrarSnapshotImmutable(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns1 := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns1", ns1); err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()
	ns2 := MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns2", ns2); err != nil {
		t.Fatal(err)
	}
	u1, err := snap.Derive("ns1", "x", DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	expected := NewSHA1(ns1, []byte("x"))
	if u1 != expected {
		t.Fatalf("snapshot changed: got %s, want %s", u1, expected)
	}
	_, err = snap.Derive("ns2", "x", DeriveV5)
	if !errors.Is(err, ErrNamespaceNotFound) {
		t.Fatalf("expected ErrNamespaceNotFound from old snapshot, got %v", err)
	}
	if _, err := r.Derive("ns2", "x", DeriveV5); err != nil {
		t.Fatalf("registrar should still resolve ns2: %v", err)
	}
}

// TestRegistrarSnapshotUpdateDoesNotModifyOld verifies that deriving a new
// snapshot (an update) does not modify the old snapshot or the registrar.
func TestRegistrarSnapshotUpdateDoesNotModifyOld(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns1 := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns1", ns1); err != nil {
		t.Fatal(err)
	}
	old := r.Snapshot()
	ns2 := MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns2", ns2); err != nil {
		t.Fatal(err)
	}
	updated := r.Snapshot()
	if _, err := old.Derive("ns2", "x", DeriveV5); !errors.Is(err, ErrNamespaceNotFound) {
		t.Fatalf("old snapshot should not resolve ns2, got %v", err)
	}
	if _, err := updated.Derive("ns2", "x", DeriveV5); err != nil {
		t.Fatalf("updated snapshot should resolve ns2: %v", err)
	}
	if _, err := r.Derive("ns1", "x", DeriveV5); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Derive("ns2", "x", DeriveV5); err != nil {
		t.Fatal(err)
	}
}

// TestRegistrarUTF8Rejected verifies that invalid UTF-8 in names or input is
// rejected before derivation.
func TestRegistrarUTF8Rejected(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns", ns); err != nil {
		t.Fatal(err)
	}
	invalid := string([]byte{0xff, 0xfe, 0xfd})
	if err := r.Register(invalid, ns); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("expected ErrInvalidUTF8 for invalid name, got %v", err)
	}
	if _, err := r.Derive("ns", invalid, DeriveV5); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("expected ErrInvalidUTF8 for invalid input, got %v", err)
	}
	if _, err := r.Derive(invalid, "x", DeriveV5); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("expected ErrInvalidUTF8 for invalid name in derive, got %v", err)
	}
}

// TestRegistrarUnsupportedVersion verifies that an unsupported derive version
// is rejected.
func TestRegistrarUnsupportedVersion(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns", ns); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Derive("ns", "x", DeriveVersion(99)); !errors.Is(err, ErrUnsupportedDeriveVersion) {
		t.Fatalf("expected ErrUnsupportedDeriveVersion, got %v", err)
	}
}

// TestRegistrarAliasResolution verifies that aliases resolve to the correct
// canonical namespace.
func TestRegistrarAliasResolution(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("canonical", ns); err != nil {
		t.Fatal(err)
	}
	if err := r.AddAlias("alias", "canonical"); err != nil {
		t.Fatal(err)
	}
	u1, err := r.Derive("canonical", "x", DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	u2, err := r.Derive("alias", "x", DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	if u1 != u2 {
		t.Fatalf("alias did not resolve to canonical: got %s and %s", u1, u2)
	}
}

// TestRegistrarV3AndV5Differ verifies that v3 and v5 produce different UUIDs
// for the same namespace and input.
func TestRegistrarV3AndV5Differ(t *testing.T) {
	r := NewNamespaceRegistrar()
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if err := r.Register("ns", ns); err != nil {
		t.Fatal(err)
	}
	v3, err := r.Derive("ns", "x", DeriveV3)
	if err != nil {
		t.Fatal(err)
	}
	v5, err := r.Derive("ns", "x", DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	if v3 == v5 {
		t.Fatalf("expected v3 and v5 to differ, got %s", v3)
	}
	if v3.Version() != 3 {
		t.Fatalf("expected v3, got %d", v3.Version())
	}
	if v5.Version() != 5 {
		t.Fatalf("expected v5, got %d", v5.Version())
	}
}

// TestRegistrarNewSHA1Unchanged verifies that the ordinary NewSHA1 function
// is unchanged and still produces RFC 9562 version-5 UUIDs.
func TestRegistrarNewSHA1Unchanged(t *testing.T) {
	ns := MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	u := NewSHA1(ns, []byte("example.com"))
	if u.Version() != 5 {
		t.Fatalf("expected version 5, got %d", u.Version())
	}
	if u.Variant() != RFC4122 {
		t.Fatalf("expected RFC4122 variant, got %v", u.Variant())
	}
	// Verify determinism: same input produces the same UUID.
	u2 := NewSHA1(ns, []byte("example.com"))
	if u != u2 {
		t.Fatalf("NewSHA1 not deterministic: got %s and %s", u, u2)
	}
	// Verify that NewSHA1 matches the registrar's DeriveV5 path.
	r := NewNamespaceRegistrar()
	if err := r.Register("ns", ns); err != nil {
		t.Fatal(err)
	}
	derived, err := r.Derive("ns", "example.com", DeriveV5)
	if err != nil {
		t.Fatal(err)
	}
	if u != derived {
		t.Fatalf("NewSHA1 and DeriveV5 differ: got %s and %s", u, derived)
	}
}
