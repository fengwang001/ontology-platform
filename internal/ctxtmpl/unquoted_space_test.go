package ctxtmpl

import (
	"fmt"
	"strings"
	"testing"
)

// TestUnquotedAttrEscapesEveryWhitespace renders every byte the module
// classifies as ASCII whitespace (isASCIISpace) into an unquoted attribute
// and requires it to be entity-escaped, so it cannot terminate the value and
// splice in a new attribute. Before the fix, '\f' and '\v' leaked through.
func TestUnquotedAttrEscapesEveryWhitespace(t *testing.T) {
	for b := 0; b < 256; b++ {
		ch := byte(b)
		if !isASCIISpace(ch) {
			continue
		}
		t.Run(fmt.Sprintf("byte 0x%02X", ch), func(t *testing.T) {
			got, err := Render(`<a class={{x}}>`, map[string]string{"x": "a" + string(ch) + "onclick=alert(1)"})
			if err != nil {
				t.Fatal(err)
			}
			valuePart := got[len(`<a class=`) : len(got)-1]
			if strings.Contains(valuePart, string(ch)) {
				t.Fatalf("whitespace 0x%02x rendered raw in value: %q", ch, got)
			}
			if strings.Contains(got, " onclick=") || strings.Contains(got, string(ch)+"onclick=") {
				t.Fatalf("whitespace 0x%02x allowed attribute injection: %q", ch, got)
			}
			wantEntity := "&#" + itoa(int(ch)) + ";"
			if !strings.Contains(valuePart, wantEntity) {
				t.Fatalf("whitespace 0x%02x not escaped as %s: %q", ch, wantEntity, got)
			}
		})
	}
}

// TestWhitespaceDefinitionParity asserts that the scanner's whitespace
// definition (isASCIISpace, used to find where an unquoted value ends) and
// the unquoted-attribute escaper's whitespace set are exactly equal, so a
// change to either side is caught immediately. Whitespace entries are the
// only escaper entries at or below 0x20; every other entry is printable
// punctuation.
func TestWhitespaceDefinitionParity(t *testing.T) {
	esc := escaperFor(ctxAttrUnquoted)
	for b := 0; b < 256; b++ {
		ch := byte(b)
		_, inEscaper := esc[ch]
		escaperWhitespace := inEscaper && ch <= ' '
		if isASCIISpace(ch) != escaperWhitespace {
			t.Errorf("byte 0x%02x: isASCIISpace=%t but escaper whitespace=%t", ch, isASCIISpace(ch), escaperWhitespace)
		}
	}
}

// TestUnquotedAttrKeepsNormalValues guards against over-escaping: ordinary
// unquoted values must render byte-for-byte.
func TestUnquotedAttrKeepsNormalValues(t *testing.T) {
	got, err := Render(`<a class={{x}}>`, map[string]string{"x": "abc-123_x"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<a class=abc-123_x>`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
