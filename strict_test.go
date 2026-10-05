// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"errors"
	"fmt"
	"testing"
)

// Targeted test command for the strict protocol parser:
//
//	go test -run TestStrict -v
//	go test -run ExampleParseStrict -v
//
// The four text protocols (canonical, URN, braced, plain) all decode to the
// same UUID bytes but report different source formats.
func TestStrictParseFourFormats(t *testing.T) {
	want := MustParse("f47ac10b-58cc-4372-8567-0e02b2c3d479")

	cases := []struct {
		in     string
		format Format
	}{
		{"f47ac10b-58cc-4372-8567-0e02b2c3d479", FormatCanonical},
		{"urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479", FormatURN},
		{"{f47ac10b-58cc-4372-8567-0e02b2c3d479}", FormatBraced},
		{"f47ac10b58cc437285670e02b2c3d479", FormatPlain},
	}

	for _, tc := range cases {
		u, src, err := ParseStrict(tc.in, ParseOptions{})
		if err != nil {
			t.Errorf("ParseStrict(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if u != want {
			t.Errorf("ParseStrict(%q) = %v, want %v", tc.in, u, want)
		}
		if src != tc.format {
			t.Errorf("ParseStrict(%q) source = %s, want %s", tc.in, src, tc.format)
		}
		if got := u.String(); got != "f47ac10b-58cc-4372-8567-0e02b2c3d479" {
			t.Errorf("ParseStrict(%q).String() = %q, want canonical", tc.in, got)
		}
	}
}

// Disabling the URN category must produce a category rejection naming URN.
func TestStrictParseFormatNotAllowed(t *testing.T) {
	in := "urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479"
	allowed := FormatCanonical | FormatBraced | FormatPlain

	u, src, err := ParseStrict(in, ParseOptions{Formats: allowed})
	if err == nil {
		t.Fatalf("ParseStrict(%q) expected format-not-allowed error, got nil", in)
	}
	if !errors.Is(err, FormatNotAllowedError{}) {
		t.Errorf("ParseStrict(%q) error = %v, want FormatNotAllowedError", in, err)
	}
	var nae FormatNotAllowedError
	if !errors.As(err, &nae) {
		t.Fatalf("ParseStrict(%q) error %v is not FormatNotAllowedError", in, err)
	}
	if nae.Format != FormatURN {
		t.Errorf("FormatNotAllowedError.Format = %s, want urn", nae.Format)
	}
	if src != FormatURN {
		t.Errorf("returned source = %s, want urn", src)
	}
	if u != (UUID{}) {
		t.Errorf("returned UUID = %v, want zero on rejection", u)
	}
}

// Every category can be independently disabled.
func TestStrictParseEachFormatRejected(t *testing.T) {
	cases := []struct {
		in     string
		format Format
	}{
		{"f47ac10b-58cc-4372-8567-0e02b2c3d479", FormatCanonical},
		{"urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479", FormatURN},
		{"{f47ac10b-58cc-4372-8567-0e02b2c3d479}", FormatBraced},
		{"f47ac10b58cc437285670e02b2c3d479", FormatPlain},
	}
	for _, tc := range cases {
		allowed := FormatAny &^ tc.format
		_, _, err := ParseStrict(tc.in, ParseOptions{Formats: allowed})
		if !errors.Is(err, FormatNotAllowedError{}) {
			t.Errorf("ParseStrict(%q) with %s disabled: error = %v, want FormatNotAllowedError", tc.in, tc.format, err)
		}
	}
}

// Trailing (and leading) characters must fail explicitly; nothing is trimmed.
func TestStrictParseTrailingCharacters(t *testing.T) {
	valid := "f47ac10b-58cc-4372-8567-0e02b2c3d479"
	urn := "urn:uuid:" + valid
	braced := "{" + valid + "}"
	plain := "f47ac10b58cc437285670e02b2c3d479"

	bad := []string{
		valid + " ",
		valid + "x",
		" " + valid,
		valid + "\n",
		urn + "x",
		braced + "x",
		plain + "x",
		"x" + plain,
		"{" + valid,
		valid + "}",
		"urn:uuid:" + valid + " ",
	}
	for _, s := range bad {
		if _, _, err := ParseStrict(s, ParseOptions{}); err == nil {
			t.Errorf("ParseStrict(%q) expected error for trailing/leading content, got nil", s)
		}
	}
}

// Constraints are checked after parsing, independently of each other.
func TestStrictParseConstraints(t *testing.T) {
	in := "f47ac10b-58cc-4372-8567-0e02b2c3d479"

	if _, _, err := ParseStrict(in, ParseOptions{Version: 4, Variant: RFC4122}); err != nil {
		t.Errorf("ParseStrict with matching constraints: unexpected error %v", err)
	}

	_, _, err := ParseStrict(in, ParseOptions{Version: 5})
	if !errors.Is(err, VersionMismatchError{}) {
		t.Errorf("ParseStrict version mismatch: error = %v, want VersionMismatchError", err)
	}
	var vme VersionMismatchError
	if !errors.As(err, &vme) || vme.Actual != 4 || vme.Required != 5 {
		t.Errorf("VersionMismatchError = %+v, want Required=5 Actual=4", vme)
	}

	_, _, err = ParseStrict(in, ParseOptions{Variant: Microsoft})
	if !errors.Is(err, VariantMismatchError{}) {
		t.Errorf("ParseStrict variant mismatch: error = %v, want VariantMismatchError", err)
	}
	var varme VariantMismatchError
	if !errors.As(err, &varme) || varme.Actual != RFC4122 || varme.Required != Microsoft {
		t.Errorf("VariantMismatchError = %+v, want Required=Microsoft Actual=RFC4122", varme)
	}

	_, _, err = ParseStrict(in, ParseOptions{Version: 5, Variant: Microsoft})
	if !errors.Is(err, VersionMismatchError{}) {
		t.Errorf("ParseStrict both mismatches: error = %v, want VersionMismatchError first", err)
	}
}

// RequireVersion / RequireVariant are independent post-parse checks.
func TestStrictParseRequireMethods(t *testing.T) {
	u := MustParse("f47ac10b-58cc-4372-8567-0e02b2c3d479")
	if err := u.RequireVersion(4); err != nil {
		t.Errorf("RequireVersion(4): unexpected error %v", err)
	}
	if err := u.RequireVariant(RFC4122); err != nil {
		t.Errorf("RequireVariant(RFC4122): unexpected error %v", err)
	}
	if err := u.RequireVersion(5); !errors.Is(err, VersionMismatchError{}) {
		t.Errorf("RequireVersion(5): error = %v, want VersionMismatchError", err)
	}
	if err := u.RequireVariant(Microsoft); !errors.Is(err, VariantMismatchError{}) {
		t.Errorf("RequireVariant(Microsoft): error = %v, want VariantMismatchError", err)
	}
}

// A rejected parse must return the zero UUID and never partially fill it.
func TestStrictParseRejectReturnsZero(t *testing.T) {
	valid := "f47ac10b-58cc-4372-8567-0e02b2c3d479"
	for _, s := range []string{valid + "x", "not-a-uuid", "urn:uuid:" + valid + "x"} {
		u, _, err := ParseStrict(s, ParseOptions{})
		if err == nil {
			t.Errorf("ParseStrict(%q) expected error", s)
			continue
		}
		if u != (UUID{}) {
			t.Errorf("ParseStrict(%q) returned non-zero UUID %v on error", s, u)
		}
	}
}

// The default Parse remains permissive and unchanged.
func TestStrictParseDefaultParseUnchanged(t *testing.T) {
	for _, s := range []string{
		"f47ac10b58cc437285670e02b2c3d479",
		"{f47ac10b-58cc-4372-8567-0e02b2c3d479}",
		"urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479",
	} {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q) should remain permissive, got error %v", s, err)
		}
	}
}

// Uppercase hex digits are accepted in every protocol; the result is always
// the canonical lowercase form.
func TestStrictParseUppercaseAccepted(t *testing.T) {
	want := MustParse("f47ac10b-58cc-4372-8567-0e02b2c3d479")
	for _, s := range []string{
		"F47AC10B-58CC-4372-8567-0E02B2C3D479",
		"URN:UUID:F47AC10B-58CC-4372-8567-0E02B2C3D479",
		"{F47AC10B-58CC-4372-8567-0E02B2C3D479}",
		"F47AC10B58CC437285670E02B2C3D479",
	} {
		u, _, err := ParseStrict(s, ParseOptions{})
		if err != nil {
			t.Errorf("ParseStrict(%q) unexpected error: %v", s, err)
			continue
		}
		if u != want {
			t.Errorf("ParseStrict(%q) = %v, want %v", s, u, want)
		}
	}
}

func ExampleParseStrict_canonical() {
	u, src, err := ParseStrict("f47ac10b-58cc-4372-8567-0e02b2c3d479", ParseOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(u, src)
	// Output: f47ac10b-58cc-4372-8567-0e02b2c3d479 canonical
}

func ExampleParseStrict_urn() {
	u, src, err := ParseStrict("urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479", ParseOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(u, src)
	// Output: f47ac10b-58cc-4372-8567-0e02b2c3d479 urn
}

func ExampleParseStrict_braced() {
	u, src, err := ParseStrict("{f47ac10b-58cc-4372-8567-0e02b2c3d479}", ParseOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(u, src)
	// Output: f47ac10b-58cc-4372-8567-0e02b2c3d479 braced
}

func ExampleParseStrict_plain() {
	u, src, err := ParseStrict("f47ac10b58cc437285670e02b2c3d479", ParseOptions{})
	if err != nil {
		panic(err)
	}
	fmt.Println(u, src)
	// Output: f47ac10b-58cc-4372-8567-0e02b2c3d479 plain
}

func ExampleParseStrict_rejectURN() {
	allowed := FormatCanonical
	_, src, err := ParseStrict("urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479", ParseOptions{Formats: allowed})
	fmt.Println(src, err)
	// Output: urn uuid: format "urn" is not allowed
}

func ExampleParseStrict_trailingCharacters() {
	_, _, err := ParseStrict("f47ac10b-58cc-4372-8567-0e02b2c3d479 ", ParseOptions{})
	fmt.Println(err)
	// Output: invalid UUID length: 37
}

func ExampleParseStrict_constraints() {
	in := "f47ac10b-58cc-4372-8567-0e02b2c3d479"
	_, _, err := ParseStrict(in, ParseOptions{Version: 4, Variant: RFC4122})
	fmt.Println(err)
	_, _, err = ParseStrict(in, ParseOptions{Version: 5})
	fmt.Println(err)
	// Output:
	// <nil>
	// uuid: version mismatch: required 5, got 4
}

func ExampleUUID_RequireVersion() {
	u := MustParse("f47ac10b-58cc-4372-8567-0e02b2c3d479")
	fmt.Println(u.RequireVersion(4))
	fmt.Println(u.RequireVersion(5))
	// Output:
	// <nil>
	// uuid: version mismatch: required 5, got 4
}
