// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"fmt"
	"strings"
)

// Format is a bitmask identifying the textual protocol a UUID was received
// from. Callers pass a bitmask of permitted formats to ParseStrict; the
// returned Format is the single protocol the text was actually parsed from.
//
// The four supported text protocols are:
//
//	FormatCanonical: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 bytes)
//	FormatURN:       urn:uuid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (45 bytes)
//	FormatBraced:    {xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx} (38 bytes)
//	FormatPlain:     xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx (32 bytes)
type Format uint

const (
	// FormatCanonical is the canonical hyphenated form,
	// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 bytes).
	FormatCanonical Format = 1 << iota

	// FormatURN is the RFC 2141 URN form,
	// urn:uuid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (45 bytes).
	FormatURN

	// FormatBraced is the Microsoft-style braced form,
	// {xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx} (38 bytes).
	FormatBraced

	// FormatPlain is the raw 32 hex digit form without hyphens,
	// xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx (32 bytes).
	FormatPlain
)

// FormatAny permits every defined text protocol.
const FormatAny = FormatCanonical | FormatURN | FormatBraced | FormatPlain

// String returns the name of a single format bit.
func (f Format) String() string {
	switch f {
	case FormatCanonical:
		return "canonical"
	case FormatURN:
		return "urn"
	case FormatBraced:
		return "braced"
	case FormatPlain:
		return "plain"
	default:
		return fmt.Sprintf("Format(%d)", uint(f))
	}
}

// ParseOptions configures ParseStrict.
type ParseOptions struct {
	// Formats is the set of text protocols that may be parsed. A zero value
	// permits every format (FormatAny).
	Formats Format

	// Version, if non-zero, requires the parsed UUID to have this version.
	// The check is performed after parsing.
	Version Version

	// Variant, if not Invalid, requires the parsed UUID to have this
	// variant. The check is performed after parsing.
	Variant Variant
}

// FormatNotAllowedError is returned when a UUID's text protocol is not in the
// set of permitted formats.
type FormatNotAllowedError struct {
	Format Format
}

func (e FormatNotAllowedError) Error() string {
	return fmt.Sprintf("uuid: format %q is not allowed", e.Format)
}

func (e FormatNotAllowedError) Is(target error) bool {
	_, ok := target.(FormatNotAllowedError)
	return ok
}

// VersionMismatchError is returned when a parsed UUID's version does not
// match the required version.
type VersionMismatchError struct {
	Required Version
	Actual   Version
}

func (e VersionMismatchError) Error() string {
	return fmt.Sprintf("uuid: version mismatch: required %d, got %d", e.Required, e.Actual)
}

func (e VersionMismatchError) Is(target error) bool {
	_, ok := target.(VersionMismatchError)
	return ok
}

// VariantMismatchError is returned when a parsed UUID's variant does not
// match the required variant.
type VariantMismatchError struct {
	Required Variant
	Actual   Variant
}

func (e VariantMismatchError) Error() string {
	return fmt.Sprintf("uuid: variant mismatch: required %s, got %s", e.Required, e.Actual)
}

func (e VariantMismatchError) Is(target error) bool {
	_, ok := target.(VariantMismatchError)
	return ok
}

// ParseStrict decodes s into a UUID using an explicit text protocol selected
// by the caller.
//
// Only the formats whose bits are set in opts.Formats are accepted; a zero
// mask permits every format (FormatAny). The returned format identifies the
// protocol the text was parsed from. The entire input must be a valid UUID in
// one of the permitted formats; leading or trailing characters are rejected
// rather than trimmed.
//
// If opts.Version is non-zero the parsed UUID's version must equal it, and if
// opts.Variant is not Invalid the parsed UUID's variant must equal it. Both
// constraints are checked independently after parsing.
//
// On error the returned UUID is the zero UUID; the caller's target is never
// modified. The default Parse remains unchanged for backwards compatibility.
func ParseStrict(s string, opts ParseOptions) (UUID, Format, error) {
	allowed := opts.Formats
	if allowed == 0 {
		allowed = FormatAny
	}

	var (
		uuid   UUID
		format Format
		body   string
	)

	switch len(s) {
	case 36:
		format = FormatCanonical
		body = s
	case 45:
		if !strings.EqualFold(s[:9], "urn:uuid:") {
			return UUID{}, 0, URNPrefixError{s[:9]}
		}
		format = FormatURN
		body = s[9:]
	case 38:
		if s[0] != '{' || s[len(s)-1] != '}' {
			return UUID{}, 0, ErrInvalidBracketedFormat
		}
		format = FormatBraced
		body = s[1 : len(s)-1]
	case 32:
		format = FormatPlain
		body = s
	default:
		return UUID{}, 0, invalidLengthError{len(s)}
	}

	if allowed&format == 0 {
		return UUID{}, format, FormatNotAllowedError{format}
	}

	if format == FormatPlain {
		for i := 0; i < 32; i += 2 {
			v, ok := xtob(body[i], body[i+1])
			if !ok {
				return UUID{}, format, ErrInvalidUUIDFormat
			}
			uuid[i/2] = v
		}
	} else {
		if body[8] != '-' || body[13] != '-' || body[18] != '-' || body[23] != '-' {
			return UUID{}, format, ErrInvalidUUIDFormat
		}
		for i, x := range [16]int{
			0, 2, 4, 6,
			9, 11,
			14, 16,
			19, 21,
			24, 26, 28, 30, 32, 34,
		} {
			v, ok := xtob(body[x], body[x+1])
			if !ok {
				return UUID{}, format, ErrInvalidUUIDFormat
			}
			uuid[i] = v
		}
	}

	if opts.Version != 0 && uuid.Version() != opts.Version {
		return UUID{}, format, VersionMismatchError{Required: opts.Version, Actual: uuid.Version()}
	}
	if opts.Variant != Invalid && uuid.Variant() != opts.Variant {
		return UUID{}, format, VariantMismatchError{Required: opts.Variant, Actual: uuid.Variant()}
	}

	return uuid, format, nil
}

// RequireVersion returns a VersionMismatchError if uuid.Version() is not v.
// It is an independent post-parse constraint check.
func (uuid UUID) RequireVersion(v Version) error {
	if uuid.Version() != v {
		return VersionMismatchError{Required: v, Actual: uuid.Version()}
	}
	return nil
}

// RequireVariant returns a VariantMismatchError if uuid.Variant() is not v.
// It is an independent post-parse constraint check.
func (uuid UUID) RequireVariant(v Variant) error {
	if uuid.Variant() != v {
		return VariantMismatchError{Required: v, Actual: uuid.Variant()}
	}
	return nil
}
