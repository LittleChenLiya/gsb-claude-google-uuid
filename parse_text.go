// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"fmt"
)

// TextFormat identifies the textual representation (wire format) a UUID
// string was encoded in. It is reported by ParseText so callers can tell
// exactly which protocol form the input used.
type TextFormat int

const (
	// FormatCanonical is the canonical lowercase hyphenated form:
	// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	FormatCanonical TextFormat = iota
	// FormatURN is the RFC 2141 URN form:
	// urn:uuid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	FormatURN
	// FormatBraced is the "Microsoft style" braced form:
	// {xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}
	FormatBraced
	// FormatRaw is the hyphen-less raw hex form:
	// xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
	FormatRaw
)

func (f TextFormat) String() string {
	switch f {
	case FormatCanonical:
		return "canonical"
	case FormatURN:
		return "urn"
	case FormatBraced:
		return "braced"
	case FormatRaw:
		return "raw"
	}
	return fmt.Sprintf("TextFormat(%d)", int(f))
}

// FormatMask is a bitmask selecting which TextFormats a parser accepts.
type FormatMask uint8

const (
	MaskCanonical FormatMask = 1 << iota
	MaskURN
	MaskBraced
	MaskRaw
	// MaskAll allows every known text format.
	MaskAll = MaskCanonical | MaskURN | MaskBraced | MaskRaw
)

// maskOf returns the bit corresponding to f.
func maskOf(f TextFormat) FormatMask {
	return 1 << uint(f)
}

// ParseOptions controls the behavior of ParseText and UUID.DecodeText.
// The zero value accepts all four text formats and applies no
// version or variant constraints.
type ParseOptions struct {
	// Formats selects the accepted input formats. A zero Formats value
	// means MaskAll. Inputs matching a disabled format are rejected with
	// a FormatNotAllowedError even if they are otherwise well formed.
	Formats FormatMask

	// CheckVersion, when true, requires the parsed UUID to have
	// version RequireVersion. The check runs after parsing and is
	// independent of the variant check.
	CheckVersion   bool
	RequireVersion Version

	// CheckVariant, when true, requires the parsed UUID to have
	// variant RequireVariant. The check runs after parsing and is
	// independent of the version check.
	CheckVariant   bool
	RequireVariant Variant
}

// FormatNotAllowedError is returned when the input matches a text format
// that the caller did not enable in ParseOptions.Formats.
type FormatNotAllowedError struct{ Format TextFormat }

func (e FormatNotAllowedError) Error() string {
	return fmt.Sprintf("uuid: %s text format not allowed", e.Format)
}

func (e FormatNotAllowedError) Is(target error) bool {
	_, ok := target.(FormatNotAllowedError)
	return ok
}

// ErrFormatNotAllowed matches any FormatNotAllowedError via errors.Is.
var ErrFormatNotAllowed = FormatNotAllowedError{}

// VersionMismatchError is returned when a parsed UUID fails the
// ParseOptions.CheckVersion constraint.
type VersionMismatchError struct {
	Got, Want Version
}

func (e VersionMismatchError) Error() string {
	return fmt.Sprintf("uuid: version mismatch: got %s, want %s", e.Got, e.Want)
}

func (e VersionMismatchError) Is(target error) bool {
	_, ok := target.(VersionMismatchError)
	return ok
}

// ErrVersionMismatch matches any VersionMismatchError via errors.Is.
var ErrVersionMismatch = VersionMismatchError{}

// VariantMismatchError is returned when a parsed UUID fails the
// ParseOptions.CheckVariant constraint.
type VariantMismatchError struct {
	Got, Want Variant
}

func (e VariantMismatchError) Error() string {
	return fmt.Sprintf("uuid: variant mismatch: got %s, want %s", e.Got, e.Want)
}

func (e VariantMismatchError) Is(target error) bool {
	_, ok := target.(VariantMismatchError)
	return ok
}

// ErrVariantMismatch matches any VariantMismatchError via errors.Is.
var ErrVariantMismatch = VariantMismatchError{}

// ParseText parses s using the explicit text protocol described by opts and
// returns the decoded UUID together with the TextFormat the input used.
//
// Unlike Parse, ParseText is strict about the protocol:
//   - The input must be complete: nothing is trimmed, so leading or
//     trailing characters (including whitespace) make the parse fail.
//   - Hex digits must be lowercase, matching the canonical output form.
//   - The URN prefix must be exactly "urn:uuid:".
//   - The caller selects the allowed formats via opts.Formats; a well
//     formed input in a disabled format is rejected with
//     FormatNotAllowedError.
//   - Version and variant constraints (opts.CheckVersion /
//     opts.CheckVariant) are verified independently after parsing.
//
// On any error the returned UUID is the zero UUID; a rejection never
// produces a partially decoded value.
func ParseText(s string, opts ParseOptions) (UUID, TextFormat, error) {
	var u UUID
	format, err := u.DecodeText(s, opts)
	if err != nil {
		return UUID{}, format, err
	}
	return u, format, nil
}

// DecodeText parses s into uuid using the explicit text protocol described
// by opts and reports which TextFormat the input used. It behaves exactly
// like ParseText, but writes into an existing UUID: on any error *uuid is
// left completely unmodified.
func (uuid *UUID) DecodeText(s string, opts ParseOptions) (TextFormat, error) {
	format, body, err := splitTextFormat(s)
	if err != nil {
		return format, err
	}
	formats := opts.Formats
	if formats == 0 {
		formats = MaskAll
	}
	if formats&maskOf(format) == 0 {
		return format, FormatNotAllowedError{format}
	}
	var decoded UUID
	if err := decodeCanonicalBody(&decoded, body, format == FormatRaw); err != nil {
		return format, err
	}
	// Version and variant constraints are checked independently, after
	// the text has been fully parsed.
	if opts.CheckVersion && decoded.Version() != opts.RequireVersion {
		return format, VersionMismatchError{Got: decoded.Version(), Want: opts.RequireVersion}
	}
	if opts.CheckVariant && decoded.Variant() != opts.RequireVariant {
		return format, VariantMismatchError{Got: decoded.Variant(), Want: opts.RequireVariant}
	}
	*uuid = decoded
	return format, nil
}

// splitTextFormat classifies s by its exact length and framing and returns
// the detected format plus the 36 (or 32) character hex body. The input is
// never trimmed: any extra leading or trailing content changes the length
// and is reported as an invalidLengthError.
func splitTextFormat(s string) (TextFormat, string, error) {
	switch len(s) {
	// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	case 36:
		return FormatCanonical, s, nil

	// urn:uuid:xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	case 36 + 9:
		if s[:9] != "urn:uuid:" {
			return FormatURN, "", URNPrefixError{s[:9]}
		}
		return FormatURN, s[9:], nil

	// {xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}
	case 36 + 2:
		if s[0] != '{' || s[len(s)-1] != '}' {
			return FormatBraced, "", ErrInvalidBracketedFormat
		}
		return FormatBraced, s[1 : len(s)-1], nil

	// xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
	case 32:
		return FormatRaw, s, nil

	default:
		return FormatCanonical, "", invalidLengthError{len(s)}
	}
}

// decodeCanonicalBody decodes the hex body of a UUID text form into uuid.
// body must be 36 hyphenated characters, or 32 characters when raw is true.
// Only lowercase hex digits are accepted.
func decodeCanonicalBody(uuid *UUID, body string, raw bool) error {
	if raw {
		for i := range uuid {
			v, ok := xtobLower(body[i*2], body[i*2+1])
			if !ok {
				return ErrInvalidUUIDFormat
			}
			uuid[i] = v
		}
		return nil
	}
	if body[8] != '-' || body[13] != '-' || body[18] != '-' || body[23] != '-' {
		return ErrInvalidUUIDFormat
	}
	for i, x := range [16]int{
		0, 2, 4, 6,
		9, 11,
		14, 16,
		19, 21,
		24, 26, 28, 30, 32, 34,
	} {
		v, ok := xtobLower(body[x], body[x+1])
		if !ok {
			return ErrInvalidUUIDFormat
		}
		uuid[i] = v
	}
	return nil
}

// xtobLower converts two lowercase hex characters to a byte. Unlike xtob it
// rejects uppercase digits so that only the canonical encoding is accepted.
func xtobLower(x1, x2 byte) (byte, bool) {
	b1, ok := hexValueLower(x1)
	if !ok {
		return 0, false
	}
	b2, ok := hexValueLower(x2)
	if !ok {
		return 0, false
	}
	return b1<<4 | b2, true
}

func hexValueLower(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}
