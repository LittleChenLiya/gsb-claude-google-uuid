// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"errors"
	"strings"
	"testing"
)

func mustRegistry(t *testing.T, version int) *Registry {
	t.Helper()
	r, err := NewRegistry(version)
	if err != nil {
		t.Fatalf("NewRegistry(%d): %v", version, err)
	}
	return r
}

// Two namespaces deriving the same name bytes must produce different UUIDs.
func TestRegistrySameNameDifferentNamespaces(t *testing.T) {
	r := mustRegistry(t, 5)
	if err := r.Register("app", NameSpaceDNS); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("web", NameSpaceURL); err != nil {
		t.Fatal(err)
	}
	a, err := r.Derive("app", "python.org")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Derive("web", "python.org")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("same input in different namespaces produced identical UUIDs: %s", a)
	}
	// And they must match the plain package-level derivation for those spaces.
	if want := NewSHA1(NameSpaceDNS, []byte("python.org")); a != want {
		t.Fatalf("Derive(app) = %s, want %s", a, want)
	}
	if want := NewSHA1(NameSpaceURL, []byte("python.org")); b != want {
		t.Fatalf("Derive(web) = %s, want %s", b, want)
	}
}

// Repeated derivation of the same input under the same namespace is stable.
func TestRegistryDeterministic(t *testing.T) {
	for _, version := range []int{3, 5} {
		r := mustRegistry(t, version)
		if err := r.Register("app", NameSpaceDNS); err != nil {
			t.Fatal(err)
		}
		first, err := r.Derive("app", "重复输入")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			got, err := r.Derive("app", "重复输入")
			if err != nil {
				t.Fatal(err)
			}
			if got != first {
				t.Fatalf("version %d: derive not deterministic: %s vs %s", version, got, first)
			}
		}
		if v := first.Version(); int(v) != version {
			t.Fatalf("derived UUID version = %d, want %d", v, version)
		}
	}
}

// Same name with a different UUID is a conflict, reported first.
func TestRegistryConflictFirst(t *testing.T) {
	r := mustRegistry(t, 5)
	if err := r.Register("app", NameSpaceDNS); err != nil {
		t.Fatal(err)
	}
	// Idempotent re-registration with the same UUID is fine.
	if err := r.Register("app", NameSpaceDNS); err != nil {
		t.Fatalf("idempotent register: %v", err)
	}
	err := r.Register("app", NameSpaceURL)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ConflictError, got %v", err)
	}
	if ce.Existing != NameSpaceDNS || ce.Incoming != NameSpaceURL {
		t.Fatalf("conflict carries wrong UUIDs: %+v", ce)
	}
	// The original binding is unchanged.
	id, err := r.Resolve("app")
	if err != nil || id != NameSpaceDNS {
		t.Fatalf("after conflict Resolve = %s, %v", id, err)
	}
}

// Alias cycles are rejected and the chain is reported.
func TestRegistryAliasCycle(t *testing.T) {
	r := mustRegistry(t, 5)
	if err := r.Register("root", NameSpaceDNS); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAlias("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAlias("b", "c"); err != nil {
		t.Fatal(err)
	}
	err := r.RegisterAlias("c", "a")
	var cyc *AliasCycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected *AliasCycleError, got %v", err)
	}
	chain := strings.Join(cyc.Chain, "->")
	if chain != "c->a->b->c" {
		t.Fatalf("cycle chain = %q, want %q", chain, "c->a->b->c")
	}
	if !strings.Contains(err.Error(), "c -> a -> b -> c") {
		t.Fatalf("error message missing chain: %v", err)
	}
	// The rejected edge was rolled back: c must not resolve.
	if _, err := r.Resolve("c"); !errors.Is(err, ErrUnknownNamespace) {
		t.Fatalf("after rollback Resolve(c) err = %v, want ErrUnknownNamespace", err)
	}
	// Self-cycle is rejected too.
	if err := r.RegisterAlias("s", "s"); err == nil {
		t.Fatal("self alias cycle accepted")
	}
	// A valid alias still resolves and derives like its target.
	if err := r.RegisterAlias("alias-root", "root"); err != nil {
		t.Fatal(err)
	}
	direct, err := r.Derive("root", "data")
	if err != nil {
		t.Fatal(err)
	}
	viaAlias, err := r.Derive("alias-root", "data")
	if err != nil {
		t.Fatal(err)
	}
	if direct != viaAlias {
		t.Fatalf("alias derive %s != direct derive %s", viaAlias, direct)
	}
}

// Updating a snapshot must not change the registry it was taken from.
func TestRegistrySnapshotImmutable(t *testing.T) {
	r := mustRegistry(t, 5)
	if err := r.Register("app", NameSpaceDNS); err != nil {
		t.Fatal(err)
	}
	snap := r.Snapshot()

	// Mutate the snapshot.
	if err := snap.Register("web", NameSpaceURL); err != nil {
		t.Fatal(err)
	}
	if err := snap.RegisterAlias("a", "app"); err != nil {
		t.Fatal(err)
	}

	// The original registry is unchanged.
	rep := r.Report()
	if rep.NumNamespaces != 1 || rep.NumAliases != 0 {
		t.Fatalf("original registry changed by snapshot update: %+v", rep)
	}
	if _, err := r.Resolve("web"); !errors.Is(err, ErrUnknownNamespace) {
		t.Fatalf("original registry gained snapshot entry: %v", err)
	}

	// And later updates to the original do not change the snapshot either.
	if err := r.Register("db", NameSpaceOID); err != nil {
		t.Fatal(err)
	}
	if got := snap.Report().NumNamespaces; got != 2 {
		t.Fatalf("snapshot changed by original update: %d namespaces", got)
	}
	// Both still derive identically for the shared entry.
	d1, _ := r.Derive("app", "x")
	d2, _ := snap.Derive("app", "x")
	if d1 != d2 {
		t.Fatalf("snapshot derive mismatch: %s vs %s", d1, d2)
	}
}

// The report must not store any name contents.
func TestRegistryReportHasNoNames(t *testing.T) {
	r := mustRegistry(t, 3)
	if err := r.Register("secret-name", NameSpaceDNS); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterAlias("secret-alias", "secret-name"); err != nil {
		t.Fatal(err)
	}
	rep := r.Report()
	if rep.Version != 3 || rep.NumNamespaces != 1 || rep.NumAliases != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if len(rep.Namespaces) != 1 || rep.Namespaces[0] != NameSpaceDNS {
		t.Fatalf("report namespaces = %v", rep.Namespaces)
	}
	// The report has no string fields; formatting it must not leak names.
	if strings.Contains(rep.Namespaces[0].String(), "secret") {
		t.Fatal("report leaks name contents")
	}
}

// Invalid UTF-8 input is rejected by the explicit normalization rule.
func TestRegistryUTF8Normalization(t *testing.T) {
	r := mustRegistry(t, 5)
	bad := string([]byte{0xff, 0xfe})
	if err := r.Register(bad, NameSpaceDNS); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("Register invalid UTF-8: %v", err)
	}
	if err := r.RegisterAlias(bad, "x"); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("RegisterAlias invalid UTF-8: %v", err)
	}
	if err := r.Register("app", NameSpaceDNS); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Derive("app", bad); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("Derive invalid UTF-8: %v", err)
	}
	// Valid multi-byte UTF-8 works and is deterministic.
	if _, err := r.Derive("app", "名前空間"); err != nil {
		t.Fatalf("Derive valid UTF-8: %v", err)
	}
}

// Only versions 3 and 5 are accepted.
func TestRegistryVersionFixed(t *testing.T) {
	for _, v := range []int{0, 1, 2, 4, 6, 7, 8} {
		if _, err := NewRegistry(v); !errors.Is(err, ErrInvalidDeriveVersion) {
			t.Fatalf("NewRegistry(%d) err = %v, want ErrInvalidDeriveVersion", v, err)
		}
	}
	if r := mustRegistry(t, 3); r.Version() != 3 {
		t.Fatalf("Version() = %d", r.Version())
	}
}

// The plain package-level NewSHA1 must be unchanged (RFC 9562 test vector).
func TestNewSHA1Unchanged(t *testing.T) {
	got := NewSHA1(NameSpaceDNS, []byte("python.org"))
	want := MustParse("886313e1-3b8a-5372-9b90-0c9aee199e5d")
	if got != want {
		t.Fatalf("NewSHA1 = %s, want %s", got, want)
	}
	gotMD5 := NewMD5(NameSpaceDNS, []byte("python.org"))
	if wantMD5 := MustParse("6fa459ea-ee8a-3ca4-894e-db77e160355e"); gotMD5 != wantMD5 {
		t.Fatalf("NewMD5 = %s, want %s", gotMD5, wantMD5)
	}
}
