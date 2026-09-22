package ctxtmpl

import (
	"fmt"
	"strings"
	"testing"
)

// asciiWhitespaceSet returns every byte the package treats as ASCII
// whitespace, derived from the scanner-side predicate.
func asciiWhitespaceSet() map[byte]bool {
	set := map[byte]bool{}
	for b := 0; b < 256; b++ {
		if isASCIISpace(byte(b)) {
			set[byte(b)] = true
		}
	}
	return set
}

// TestRenderUnquotedAttrEscapesEveryWhitespace renders, for each byte the
// package classifies as whitespace, an unquoted attribute value containing
// that byte followed by a would-be attribute, and asserts the byte is
// escaped so no new attribute can be assembled.
func TestRenderUnquotedAttrEscapesEveryWhitespace(t *testing.T) {
	for b := range asciiWhitespaceSet() {
		b := b
		t.Run(fmt.Sprintf("0x%02x", b), func(t *testing.T) {
			got, err := Render("<a c={{x}}>", map[string]string{
				"x": "v" + string(b) + "onclick=pwn",
			})
			if err != nil {
				t.Fatal(err)
			}
			valuePart := got[len("<a c=") : len(got)-1]
			if strings.Contains(valuePart, string(b)) {
				t.Fatalf("whitespace byte 0x%02x leaked raw into unquoted attr: %q", b, got)
			}
			if strings.Contains(valuePart, "onclick=") {
				t.Fatalf("byte 0x%02x allowed new attribute to form: %q", b, got)
			}
			if !strings.Contains(valuePart, "&#") {
				t.Fatalf("byte 0x%02x was not entity-escaped: %q", b, got)
			}
		})
	}
}

// TestWhitespaceDefinitionsAgree asserts that the scanner's whitespace
// predicate (isASCIISpace) and the unquoted-attribute escape table recognize
// exactly the same whitespace bytes, so a future edit to either side fails
// immediately instead of reopening the unquoted-attribute escape hatch.
func TestWhitespaceDefinitionsAgree(t *testing.T) {
	scannerSide := asciiWhitespaceSet()
	// The escaper side has no whitespace label, so recover it from the live
	// table: every escaped byte in the ASCII control/space range is a
	// whitespace entry (all punctuation entries are above 0x20).
	escaperSide := map[byte]bool{}
	for b := range escaperFor(ctxAttrUnquoted) {
		if b <= 0x20 {
			escaperSide[b] = true
		}
	}
	for b := 0; b < 256; b++ {
		if scannerSide[byte(b)] != escaperSide[byte(b)] {
			t.Errorf("byte 0x%02x: isASCIISpace=%v but escaper whitespace=%v",
				byte(b), scannerSide[byte(b)], escaperSide[byte(b)])
		}
	}
}
