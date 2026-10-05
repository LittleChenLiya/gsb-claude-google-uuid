// Copyright 2026 Google Inc.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package uuid

import (
	"errors"
	"fmt"
	"testing"
)

// The same UUID rendered in the four text protocol formats.
const (
	textCanonical = "f47ac10b-58cc-4372-8567-0e02b2c3d479"
	textURN       = "urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479"
	textBraced    = "{f47ac10b-58cc-4372-8567-0e02b2c3d479}"
	textRaw       = "f47ac10b58cc437285670e02b2c3d479"
)

// TestParseTextFormats verifies that all four legal text forms decode to
// the same 16 bytes while reporting distinct source formats.
func TestParseTextFormats(t *testing.T) {
	cases := []struct {
		in     string
		format TextFormat
	}{
		{textCanonical, FormatCanonical},
		{textURN, FormatURN},
		{textBraced, FormatBraced},
		{textRaw, FormatRaw},
	}
	var want UUID
	for i, c := range cases {
		u, format, err := ParseText(c.in, ParseOptions{})
		if err != nil {
			t.Fatalf("ParseText(%q) returned error: %v", c.in, err)
		}
		if format != c.format {
			t.Errorf("ParseText(%q) format = %v, want %v", c.in, format, c.format)
		}
		if i == 0 {
			want = u
		} else if u != want {
			t.Errorf("ParseText(%q) = %v, want same bytes as %q (%v)", c.in, u, cases[0].in, want)
		}
	}
	if got := want.String(); got != textCanonical {
		t.Errorf("canonical round trip = %q, want %q", got, textCanonical)
	}
}

// TestParseTextFormatNotAllowed verifies that disabling a format category
// rejects a well formed input in that category with ErrFormatNotAllowed.
func TestParseTextFormatNotAllowed(t *testing.T) {
	noURN := ParseOptions{Formats: MaskCanonical | MaskBraced | MaskRaw}
	u, format, err := ParseText(textURN, noURN)
	if !errors.Is(err, ErrFormatNotAllowed) {
		t.Fatalf("ParseText(URN, no-URN) error = %v, want ErrFormatNotAllowed", err)
	}
	if format != FormatURN {
		t.Errorf("rejected format = %v, want %v", format, FormatURN)
	}
	if u != (UUID{}) {
		t.Errorf("rejected parse returned %v, want zero UUID", u)
	}

	cases := []struct {
		in     string
		mask   FormatMask
		format TextFormat
	}{
		{textCanonical, MaskURN | MaskBraced | MaskRaw, FormatCanonical},
		{textBraced, MaskCanonical | MaskURN | MaskRaw, FormatBraced},
		{textRaw, MaskCanonical | MaskURN | MaskBraced, FormatRaw},
	}
	for _, c := range cases {
		if _, _, err := ParseText(c.in, ParseOptions{Formats: c.mask}); !errors.Is(err, ErrFormatNotAllowed) {
			t.Errorf("ParseText(%q, mask=%04b) error = %v, want ErrFormatNotAllowed", c.in, c.mask, err)
		}
	}
	// The zero Formats value means MaskAll.
	if _, _, err := ParseText(textURN, ParseOptions{}); err != nil {
		t.Errorf("ParseText(URN, zero options) error = %v, want nil", err)
	}
}

// TestParseTextTrailingCharacters verifies that inputs are never trimmed:
// any leading or trailing content makes the parse fail explicitly.
func TestParseTextTrailingCharacters(t *testing.T) {
	bad := []string{
		textCanonical + " ",
		textCanonical + "\n",
		textCanonical + "\t",
		" " + textCanonical,
		textCanonical + "x",
		textRaw + " ",
		textURN + "\r\n",
		textBraced + "}",
	}
	for _, s := range bad {
		u, _, err := ParseText(s, ParseOptions{})
		if err == nil {
			t.Errorf("ParseText(%q) succeeded, want trailing/leading content failure", s)
			continue
		}
		if !IsInvalidLengthError(err) {
			t.Errorf("ParseText(%q) error = %v, want invalid length error", s, err)
		}
		if u != (UUID{}) {
			t.Errorf("ParseText(%q) returned %v on failure, want zero UUID", s, u)
		}
	}
}

// TestParseTextVersionConstraint verifies the version constraint is checked
// after parsing and independently of the variant constraint.
func TestParseTextVersionConstraint(t *testing.T) {
	requireV4 := ParseOptions{CheckVersion: true, RequireVersion: 4}
	if _, _, err := ParseText(textCanonical, requireV4); err != nil {
		t.Errorf("ParseText(v4, require v4) error = %v, want nil", err)
	}
	requireV7 := ParseOptions{CheckVersion: true, RequireVersion: 7}
	u, format, err := ParseText(textCanonical, requireV7)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("ParseText(v4, require v7) error = %v, want ErrVersionMismatch", err)
	}
	var vm VersionMismatchError
	if errors.As(err, &vm) && (vm.Got != 4 || vm.Want != 7) {
		t.Errorf("VersionMismatchError = %+v, want {Got:4 Want:7}", vm)
	}
	if format != FormatCanonical {
		t.Errorf("format = %v, want %v", format, FormatCanonical)
	}
	if u != (UUID{}) {
		t.Errorf("rejected parse returned %v, want zero UUID", u)
	}
}

// TestParseTextVariantConstraint verifies the variant constraint is checked
// after parsing and independently of the version constraint.
func TestParseTextVariantConstraint(t *testing.T) {
	requireRFC := ParseOptions{CheckVariant: true, RequireVariant: RFC4122}
	if _, _, err := ParseText(textCanonical, requireRFC); err != nil {
		t.Errorf("ParseText(RFC4122, require RFC4122) error = %v, want nil", err)
	}
	requireMS := ParseOptions{CheckVariant: true, RequireVariant: Microsoft}
	if _, _, err := ParseText(textCanonical, requireMS); !errors.Is(err, ErrVariantMismatch) {
		t.Errorf("ParseText(RFC4122, require Microsoft) error = %v, want ErrVariantMismatch", err)
	}
	// Both constraints together: version passes, variant fails.
	both := ParseOptions{
		CheckVersion: true, RequireVersion: 4,
		CheckVariant: true, RequireVariant: Future,
	}
	if _, _, err := ParseText(textCanonical, both); !errors.Is(err, ErrVariantMismatch) {
		t.Errorf("ParseText(v4/RFC4122, require v4/Future) error = %v, want ErrVariantMismatch", err)
	}
}

// TestParseTextRejectDoesNotModify verifies DecodeText leaves the target
// UUID untouched whenever the input is rejected.
func TestParseTextRejectDoesNotModify(t *testing.T) {
	original := MustParse(textCanonical)
	cases := []struct {
		name string
		in   string
		opts ParseOptions
	}{
		{"trailing data", textCanonical + " ", ParseOptions{}},
		{"format not allowed", textURN, ParseOptions{Formats: MaskCanonical}},
		{"version mismatch", textCanonical, ParseOptions{CheckVersion: true, RequireVersion: 7}},
		{"variant mismatch", textCanonical, ParseOptions{CheckVariant: true, RequireVariant: Microsoft}},
		{"bad hex", "f47ac10b-58cc-4372-8567-0e02b2c3d47g", ParseOptions{}},
	}
	for _, c := range cases {
		u := original
		if _, err := u.DecodeText(c.in, c.opts); err == nil {
			t.Errorf("%s: DecodeText(%q) succeeded, want error", c.name, c.in)
		}
		if u != original {
			t.Errorf("%s: DecodeText(%q) modified target to %v, want unchanged %v", c.name, c.in, u, original)
		}
	}
	// A successful decode does update the target.
	u := UUID{}
	if _, err := u.DecodeText(textRaw, ParseOptions{}); err != nil {
		t.Fatalf("DecodeText(%q) error = %v", textRaw, err)
	}
	if u != original {
		t.Errorf("DecodeText(%q) = %v, want %v", textRaw, u, original)
	}
}

// TestParseTextStrictCanonical verifies the strict protocol rejects
// non-canonical casings that the compatible Parse still accepts.
func TestParseTextStrictCanonical(t *testing.T) {
	upper := "F47AC10B-58CC-4372-8567-0E02B2C3D479"
	if _, _, err := ParseText(upper, ParseOptions{}); err == nil {
		t.Errorf("ParseText(%q) succeeded, want uppercase rejection", upper)
	}
	if _, _, err := ParseText("URN:UUID:"+textCanonical, ParseOptions{}); err == nil {
		t.Errorf("ParseText(uppercase URN prefix) succeeded, want rejection")
	}
	if _, _, err := ParseText("F47AC10B58CC437285670E02B2C3D479", ParseOptions{}); err == nil {
		t.Errorf("ParseText(uppercase raw) succeeded, want rejection")
	}
	// Parse keeps its historical, lenient behavior.
	if _, err := Parse(upper); err != nil {
		t.Errorf("Parse(%q) error = %v, want compatibility (nil)", upper, err)
	}
	if _, err := Parse("URN:UUID:" + textCanonical); err != nil {
		t.Errorf("Parse(uppercase URN) error = %v, want compatibility (nil)", err)
	}
}

// ExampleParseText demonstrates decoding the four text protocol formats of
// the same UUID. All forms yield identical bytes but report their distinct
// source format.
func ExampleParseText() {
	for _, s := range []string{
		"f47ac10b-58cc-4372-8567-0e02b2c3d479",          // canonical
		"urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479", // URN
		"{f47ac10b-58cc-4372-8567-0e02b2c3d479}",        // braced
		"f47ac10b58cc437285670e02b2c3d479",              // raw, no hyphens
	} {
		u, format, err := ParseText(s, ParseOptions{})
		fmt.Printf("%-9s %v %v\n", format, u, err)
	}
	// Output:
	// canonical f47ac10b-58cc-4372-8567-0e02b2c3d479 <nil>
	// urn       f47ac10b-58cc-4372-8567-0e02b2c3d479 <nil>
	// braced    f47ac10b-58cc-4372-8567-0e02b2c3d479 <nil>
	// raw       f47ac10b-58cc-4372-8567-0e02b2c3d479 <nil>
}

// ExampleParseText_formatSelection demonstrates how the caller restricts
// the accepted protocol formats and enforces version/variant constraints.
func ExampleParseText_formatSelection() {
	opts := ParseOptions{
		Formats:        MaskCanonical, // URNs, braces and raw hex are refused
		CheckVersion:   true,
		RequireVersion: 4,
		CheckVariant:   true,
		RequireVariant: RFC4122,
	}
	_, _, err := ParseText("urn:uuid:f47ac10b-58cc-4372-8567-0e02b2c3d479", opts)
	fmt.Println(errors.Is(err, ErrFormatNotAllowed))
	_, _, err = ParseText("f47ac10b-58cc-4372-8567-0e02b2c3d479 ", opts)
	fmt.Println(IsInvalidLengthError(err)) // trailing space is never trimmed away
	u, _, err := ParseText("f47ac10b-58cc-4372-8567-0e02b2c3d479", opts)
	fmt.Println(u, err)
	// Output:
	// true
	// true
	// f47ac10b-58cc-4372-8567-0e02b2c3d479 <nil>
}
