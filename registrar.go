// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"
)

// DeriveVersion selects the name-based UUID derivation algorithm used by a
// NamespaceRegistrar.
//
// The caller fixes the derivation version explicitly; the registrar never
// chooses one implicitly. DeriveV3 derives a version-3 UUID (MD5 over the
// namespace UUID concatenated with the UTF-8 input, RFC 9562 section 5.1)
// and DeriveV5 derives a version-5 UUID (SHA-1 over the same construction,
// RFC 9562 section 5.2).
type DeriveVersion int

const (
	// DeriveV3 derives a version-3 UUID using MD5.
	DeriveV3 DeriveVersion = 3
	// DeriveV5 derives a version-5 UUID using SHA-1.
	DeriveV5 DeriveVersion = 5
)

// NameHash is a non-reversible fingerprint of a logical name. Reports and
// errors carry a NameHash instead of the raw name so that name content is
// never persisted in a report.
type NameHash [16]byte

// hashName returns the NameHash fingerprint of name.
func hashName(name string) NameHash {
	sum := sha256.Sum256([]byte(name))
	var h NameHash
	copy(h[:], sum[:16])
	return h
}

// NamespaceRegistrar is an explicit, thread-safe registrar that maps
// canonical logical names to UUIDs.
//
// # Namespace format
//
// A namespace is identified by a canonical logical name (a UTF-8 string) and
// a UUID. The registrar derives name-based UUIDs from string input using a
// version fixed by the caller (v3 or v5). String input must be valid UTF-8;
// invalid UTF-8 is rejected before any derivation.
//
// Names may be registered at most once. Registering the same name with a
// different UUID returns a *NameConflictError and does not modify the
// registrar. Alternative names may be registered as aliases; alias chains
// that loop back on themselves are rejected with an *AliasRingError whose
// Chain field reports the ring as NameHash values so that name content is not
// stored in the report.
//
// A Snapshot provides an immutable point-in-time view of the registrar.
// Taking a snapshot does not lock the registrar, and later changes to the
// registrar do not affect the snapshot.
type NamespaceRegistrar struct {
	mu      sync.RWMutex
	names   map[string]UUID
	aliases map[string]string
}

// NewNamespaceRegistrar returns an empty NamespaceRegistrar.
func NewNamespaceRegistrar() *NamespaceRegistrar {
	return &NamespaceRegistrar{
		names:   make(map[string]UUID),
		aliases: make(map[string]string),
	}
}

// Register records that the canonical logical name maps to id.
//
// If name was already registered with a UUID different from id, Register
// returns a *NameConflictError and does not modify the registrar.
func (r *NamespaceRegistrar) Register(name string, id UUID) error {
	if !utf8.ValidString(name) {
		return ErrInvalidUTF8
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.aliases[name]; ok {
		return ErrNameAlreadyRegistered
	}
	if existing, ok := r.names[name]; ok && existing != id {
		return &NameConflictError{Name: hashName(name)}
	}
	r.names[name] = id
	return nil
}

// AddAlias records that alias is an alternative name for the canonical name.
//
// Alias resolution follows the chain of aliases until a canonical name is
// found. Adding an alias that would close a loop (an alias ring) returns an
// *AliasRingError whose Chain field reports the ring as NameHash values.
func (r *NamespaceRegistrar) AddAlias(alias, canonical string) error {
	if !utf8.ValidString(alias) || !utf8.ValidString(canonical) {
		return ErrInvalidUTF8
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.names[alias]; ok {
		return ErrNameAlreadyRegistered
	}
	resolved, ok := r.resolveLocked(canonical)
	if !ok {
		return &AliasRingError{Chain: nil}
	}
	if _, ok := r.names[resolved]; !ok {
		return ErrNamespaceNotFound
	}
	// Detect a ring: walk from canonical following aliases; if we reach
	// alias, adding alias->canonical would close a loop.
	seen := map[string]bool{alias: true}
	chain := []NameHash{hashName(alias)}
	cur := canonical
	for {
		chain = append(chain, hashName(cur))
		if cur == alias {
			return &AliasRingError{Chain: chain}
		}
		if seen[cur] {
			return &AliasRingError{Chain: chain}
		}
		seen[cur] = true
		next, ok := r.aliases[cur]
		if !ok {
			break
		}
		cur = next
	}
	r.aliases[alias] = canonical
	return nil
}

// resolveLocked follows alias chains from name to a canonical name.
// Caller must hold at least a read lock.
func (r *NamespaceRegistrar) resolveLocked(name string) (string, bool) {
	seen := map[string]bool{}
	for {
		canonical, ok := r.aliases[name]
		if !ok {
			return name, true
		}
		if seen[name] {
			return "", false
		}
		seen[name] = true
		name = canonical
	}
}

// Derive returns the name-based UUID for input within the namespace
// registered under name. The derivation version is fixed by the caller:
// DeriveV3 uses MD5 and DeriveV5 uses SHA-1.
//
// name and input must be valid UTF-8; invalid UTF-8 returns ErrInvalidUTF8.
// If name is not registered (and is not an alias for a registered name),
// ErrNamespaceNotFound is returned.
func (r *NamespaceRegistrar) Derive(name, input string, v DeriveVersion) (UUID, error) {
	if !utf8.ValidString(name) || !utf8.ValidString(input) {
		return Nil, ErrInvalidUTF8
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return deriveLocked(r.names, r.aliases, name, input, v)
}

// Snapshot returns an immutable, point-in-time view of the registrar.
// Later changes to the registrar do not affect the snapshot.
func (r *NamespaceRegistrar) Snapshot() *Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make(map[string]UUID, len(r.names))
	for k, v := range r.names {
		names[k] = v
	}
	aliases := make(map[string]string, len(r.aliases))
	for k, v := range r.aliases {
		aliases[k] = v
	}
	return &Snapshot{names: names, aliases: aliases}
}

// Snapshot is an immutable, point-in-time view of a NamespaceRegistrar.
//
// A Snapshot is safe for concurrent use. It has no mutating methods; to
// "update" a snapshot, derive a new one from the registrar.
type Snapshot struct {
	names   map[string]UUID
	aliases map[string]string
}

// Derive returns the name-based UUID for input within the namespace
// registered under name, using the snapshot's immutable view.
func (s *Snapshot) Derive(name, input string, v DeriveVersion) (UUID, error) {
	if !utf8.ValidString(name) || !utf8.ValidString(input) {
		return Nil, ErrInvalidUTF8
	}
	return deriveLocked(s.names, s.aliases, name, input, v)
}

// deriveLocked resolves name to a canonical name and derives the UUID.
// It is shared by NamespaceRegistrar.Derive and Snapshot.Derive.
func deriveLocked(names map[string]UUID, aliases map[string]string, name, input string, v DeriveVersion) (UUID, error) {
	canonical, ok := resolveFrom(aliases, name)
	if !ok {
		return Nil, &AliasRingError{Chain: nil}
	}
	id, ok := names[canonical]
	if !ok {
		return Nil, ErrNamespaceNotFound
	}
	switch v {
	case DeriveV3:
		return NewMD5(id, []byte(input)), nil
	case DeriveV5:
		return NewSHA1(id, []byte(input)), nil
	default:
		return Nil, ErrUnsupportedDeriveVersion
	}
}

// resolveFrom follows alias chains from name to a canonical name.
func resolveFrom(aliases map[string]string, name string) (string, bool) {
	seen := map[string]bool{}
	for {
		canonical, ok := aliases[name]
		if !ok {
			return name, true
		}
		if seen[name] {
			return "", false
		}
		seen[name] = true
		name = canonical
	}
}

// Errors returned by the registrar.
var (
	// ErrInvalidUTF8 is returned when a name or input is not valid UTF-8.
	ErrInvalidUTF8 = errors.New("uuid: name or input is not valid UTF-8")
	// ErrNamespaceNotFound is returned when a name is not registered.
	ErrNamespaceNotFound = errors.New("uuid: namespace not found")
	// ErrUnsupportedDeriveVersion is returned when a DeriveVersion is not v3 or v5.
	ErrUnsupportedDeriveVersion = errors.New("uuid: unsupported derive version")
	// ErrNameAlreadyRegistered is returned when an alias collides with a
	// registered canonical name.
	ErrNameAlreadyRegistered = errors.New("uuid: name already registered")
)

// NameConflictError is returned when a name is registered with a UUID
// different from the one it was already registered with. The report carries
// a NameHash, not the raw name, so name content is not stored.
type NameConflictError struct {
	Name NameHash
}

func (e *NameConflictError) Error() string {
	return fmt.Sprintf("uuid: name conflict for name hash %x", e.Name)
}

func (e *NameConflictError) Is(target error) bool {
	_, ok := target.(*NameConflictError)
	return ok
}

// AliasRingError is returned when an alias chain loops back on itself.
// Chain reports the ring as NameHash values so that name content is not
// stored in the report.
type AliasRingError struct {
	Chain []NameHash
}

func (e *AliasRingError) Error() string {
	if len(e.Chain) == 0 {
		return "uuid: alias ring detected"
	}
	msg := "uuid: alias ring detected:"
	for i, h := range e.Chain {
		if i > 0 {
			msg += " ->"
		}
		msg += fmt.Sprintf(" %x", h)
	}
	return msg
}

func (e *AliasRingError) Is(target error) bool {
	_, ok := target.(*AliasRingError)
	return ok
}
