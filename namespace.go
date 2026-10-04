// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// This file implements an explicit namespace registry. A Registry maps
// logical namespace names to UUIDs and derives name-based UUIDs (version 3
// or version 5, fixed by the caller at construction time) for UTF-8 string
// inputs.
//
// Namespace name format
// ---------------------
//
// A namespace name (and an alias) is an arbitrary non-empty UTF-8 string.
// Names are normalized with an explicit UTF-8 rule before use:
//
//   - The input MUST be valid UTF-8 as defined by RFC 3629; invalid byte
//     sequences are rejected with ErrInvalidUTF8.
//   - Valid Go strings are already in canonical (shortest-form) UTF-8, so
//     normalization is strict validation plus the identity re-encoding of
//     each rune. Two names are equal if and only if their canonical UTF-8
//     byte sequences are equal.
//
// The same canonical input bytes derived under two different registered
// namespaces produce two different UUIDs, because the namespace UUID is
// hashed ahead of the data (see NewHash).
//
// Semantics
// ---------
//
//   - Registering the same name with a different UUID is a conflict and is
//     reported before any other check (*ConflictError).
//   - Alias cycles are rejected; the offending chain is reported
//     (*AliasCycleError).
//   - Snapshot returns an immutable copy: updating the snapshot never
//     changes the registry it was taken from, and vice versa.
//   - Report returns diagnostic information that deliberately does NOT
//     store any name contents, only UUIDs and counts.
//   - The package-level NewSHA1/NewMD5 helpers are untouched by this file.

// ErrInvalidUTF8 is returned when a namespace name, alias, or derived input
// is not valid UTF-8.
var ErrInvalidUTF8 = errors.New("uuid: invalid UTF-8 input")

// ErrEmptyName is returned when a namespace name or alias is empty.
var ErrEmptyName = errors.New("uuid: empty namespace name")

// ErrInvalidDeriveVersion is returned when a registry is created with a
// derivation version other than 3 or 5.
var ErrInvalidDeriveVersion = errors.New("uuid: derivation version must be 3 or 5")

// ErrUnknownNamespace is returned when a name (after alias resolution) is
// not registered in the registry.
var ErrUnknownNamespace = errors.New("uuid: unknown namespace")

// ConflictError reports that a name is already registered with a different
// UUID. It is reported before any alias-related checks.
type ConflictError struct {
	Name     string
	Existing UUID
	Incoming UUID
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("uuid: namespace conflict: %q registered as %s, cannot re-register as %s",
		e.Name, e.Existing, e.Incoming)
}

// AliasConflictError reports that a name cannot be used because it is
// already taken by an alias (or an alias collides with a registered
// namespace name).
type AliasConflictError struct {
	Name string
}

func (e *AliasConflictError) Error() string {
	return fmt.Sprintf("uuid: namespace alias conflicts with existing entry: %q", e.Name)
}

// AliasCycleError reports that adding an alias would create a cycle.
// Chain holds the alias names in resolution order, with the first element
// repeated at the end to close the loop.
type AliasCycleError struct {
	Chain []string
}

func (e *AliasCycleError) Error() string {
	return "uuid: namespace alias cycle rejected: " + strings.Join(e.Chain, " -> ")
}

// normalizeUTF8 applies the registry's explicit UTF-8 normalization rule:
// the input must be valid UTF-8 (RFC 3629); valid input is already in
// canonical shortest-form UTF-8 and is returned unchanged.
func normalizeUTF8(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", ErrInvalidUTF8
	}
	return s, nil
}

// Registry is an explicit namespace registry. It is safe for concurrent
// use. The zero value is not usable; create one with NewRegistry.
type Registry struct {
	mu      sync.RWMutex
	version int // 3 (MD5) or 5 (SHA1), fixed at construction
	spaces  map[string]UUID
	aliases map[string]string
}

// NewRegistry returns a registry that derives UUIDs with the given version.
// The version is fixed by the caller and must be 3 (MD5) or 5 (SHA1).
func NewRegistry(version int) (*Registry, error) {
	if version != 3 && version != 5 {
		return nil, ErrInvalidDeriveVersion
	}
	return &Registry{
		version: version,
		spaces:  make(map[string]UUID),
		aliases: make(map[string]string),
	}, nil
}

// MustRegistry is like NewRegistry but panics on an invalid version.
func MustRegistry(version int) *Registry {
	r, err := NewRegistry(version)
	if err != nil {
		panic(err)
	}
	return r
}

// Version returns the derivation version (3 or 5) fixed at construction.
func (r *Registry) Version() int { return r.version }

// Register binds a logical namespace name to a UUID. Registering the same
// name with the same UUID is a no-op; registering it with a different UUID
// fails with *ConflictError, which is reported before any alias check.
func (r *Registry) Register(name string, id UUID) error {
	n, err := normalizeUTF8(name)
	if err != nil {
		return err
	}
	if n == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Conflict check first: same name, different UUID.
	if existing, ok := r.spaces[n]; ok {
		if existing != id {
			return &ConflictError{Name: n, Existing: existing, Incoming: id}
		}
		return nil
	}
	if _, ok := r.aliases[n]; ok {
		return &AliasConflictError{Name: n}
	}
	r.spaces[n] = id
	return nil
}

// RegisterAlias makes alias resolve to the namespace named target. The
// target does not need to be registered yet, but adding the alias must not
// create a cycle; cycles are rejected with *AliasCycleError and the
// registry is left unchanged.
func (r *Registry) RegisterAlias(alias, target string) error {
	a, err := normalizeUTF8(alias)
	if err != nil {
		return err
	}
	t, err := normalizeUTF8(target)
	if err != nil {
		return err
	}
	if a == "" || t == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.spaces[a]; ok {
		return &AliasConflictError{Name: a}
	}
	if existing, ok := r.aliases[a]; ok && existing == t {
		return nil // idempotent re-registration
	}
	r.aliases[a] = t
	if chain := r.findCycleLocked(a); chain != nil {
		delete(r.aliases, a) // roll back, leave registry unchanged
		return &AliasCycleError{Chain: chain}
	}
	return nil
}

// findCycleLocked follows the alias chain starting from name and returns
// the cycle (first node repeated at the end) if one exists.
func (r *Registry) findCycleLocked(start string) []string {
	seen := make(map[string]int)
	var path []string
	cur := start
	for {
		if i, ok := seen[cur]; ok {
			return append(append([]string(nil), path[i:]...), cur)
		}
		seen[cur] = len(path)
		path = append(path, cur)
		next, ok := r.aliases[cur]
		if !ok {
			return nil
		}
		cur = next
	}
}

// resolveLocked follows aliases from name to a registered namespace UUID.
func (r *Registry) resolveLocked(name string) (UUID, error) {
	cur := name
	visited := make(map[string]bool)
	for {
		if id, ok := r.spaces[cur]; ok {
			return id, nil
		}
		next, ok := r.aliases[cur]
		if !ok {
			return Nil, fmt.Errorf("%w: %q", ErrUnknownNamespace, name)
		}
		if visited[cur] {
			// Should be unreachable: cycles are rejected at registration.
			return Nil, fmt.Errorf("uuid: namespace alias cycle while resolving %q", name)
		}
		visited[cur] = true
		cur = next
	}
}

// Resolve returns the UUID bound to name, following aliases.
func (r *Registry) Resolve(name string) (UUID, error) {
	n, err := normalizeUTF8(name)
	if err != nil {
		return Nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolveLocked(n)
}

// Derive resolves the namespace name and derives a version 3 or 5 UUID
// (fixed at registry construction) from data. Data is normalized with the
// registry's explicit UTF-8 rule before hashing. The same canonical bytes
// derived under two different namespaces yield different UUIDs; repeated
// derivation of the same input under the same namespace is deterministic.
func (r *Registry) Derive(name, data string) (UUID, error) {
	space, err := r.Resolve(name)
	if err != nil {
		return Nil, err
	}
	d, err := normalizeUTF8(data)
	if err != nil {
		return Nil, err
	}
	if r.version == 3 {
		return NewMD5(space, []byte(d)), nil
	}
	return NewSHA1(space, []byte(d)), nil
}

// Snapshot returns an independent copy of the registry. Updating the
// snapshot never changes the registry it was taken from, and later updates
// to the original never change the snapshot.
func (r *Registry) Snapshot() *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := &Registry{
		version: r.version,
		spaces:  make(map[string]UUID, len(r.spaces)),
		aliases: make(map[string]string, len(r.aliases)),
	}
	for k, v := range r.spaces {
		cp.spaces[k] = v
	}
	for k, v := range r.aliases {
		cp.aliases[k] = v
	}
	return cp
}

// NamespaceReport is a diagnostic summary of a registry. It deliberately
// does NOT store any namespace or alias name contents: only the fixed
// version, the registered namespace UUIDs (sorted for determinism), and
// counts.
type NamespaceReport struct {
	Version       int
	NumNamespaces int
	NumAliases    int
	Namespaces    []UUID
}

// Report returns a name-free diagnostic summary of the registry.
func (r *Registry) Report() NamespaceReport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rep := NamespaceReport{
		Version:       r.version,
		NumNamespaces: len(r.spaces),
		NumAliases:    len(r.aliases),
		Namespaces:    make([]UUID, 0, len(r.spaces)),
	}
	for _, id := range r.spaces {
		rep.Namespaces = append(rep.Namespaces, id)
	}
	sort.Slice(rep.Namespaces, func(i, j int) bool {
		return bytes.Compare(rep.Namespaces[i][:], rep.Namespaces[j][:]) < 0
	})
	return rep
}
