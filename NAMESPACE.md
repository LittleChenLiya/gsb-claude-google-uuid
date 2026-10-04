# Namespace Registry Format

`namespace.go` adds an explicit namespace registry on top of the standard
name-based UUID derivation (`NewMD5` / `NewSHA1`, which are unchanged).

## Namespace names

- A namespace name (or alias) is an arbitrary **non-empty UTF-8 string**.
- Normalization is explicit and UTF-8-based:
  - Input MUST be valid UTF-8 per RFC 3629; invalid byte sequences are
    rejected with `ErrInvalidUTF8`.
  - Valid Go strings are already canonical (shortest-form) UTF-8, so
    normalization is strict validation plus identity re-encoding.
  - Two names are equal iff their canonical UTF-8 byte sequences are equal.

## Derivation

- The derivation version is fixed by the caller at construction:
  `NewRegistry(3)` (MD5) or `NewRegistry(5)` (SHA1); anything else fails
  with `ErrInvalidDeriveVersion`.
- `Derive(name, data)` resolves `name` (following aliases) to a namespace
  UUID and hashes `namespace-UUID || UTF-8(data)` exactly like `NewHash`.
  Therefore the same input bytes under two different namespaces always
  produce two different UUIDs, and repeated derivation of the same input
  under the same namespace is deterministic.

## Registration semantics

- `Register(name, id)`:
  - Same name + same UUID: idempotent no-op.
  - Same name + different UUID: `*ConflictError`, reported **before** any
    alias check; the existing binding is kept.
  - Name already taken by an alias: `*AliasConflictError`.
- `RegisterAlias(alias, target)`:
  - Alias cycles (including self-cycles) are rejected with
    `*AliasCycleError`; the error reports the full chain
    (e.g. `c -> a -> b -> c`) and the registry is left unchanged
    (the offending edge is rolled back).
- `Snapshot()` returns an independent deep copy: updating the snapshot
  never changes the source registry, and vice versa.
- `Report()` returns a `NamespaceReport` containing only the fixed version,
  counts, and the sorted namespace UUIDs — it deliberately stores **no**
  name contents.
