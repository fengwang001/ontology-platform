package ctxtmpl

import (
	"strings"
	"testing"
)

// asciiSpaceSet returns every byte the scanner treats as ASCII whitespace.
func asciiSpaceSet() []byte {
	var out []byte
	for b := 0; b < 256; b++ {
		if isASCIISpace(byte(b)) {
			out = append(out, byte(b))
		}
	}
	return out
}

// TestUnquotedAttrEscapesEveryWhitespace renders, for each byte the module
// recognizes as ASCII whitespace, an unquoted attribute value shaped like an
// attribute-injection attempt ("1<ws>onmouseover=evil") and asserts the
// whitespace byte is escaped so it cannot terminate the value and open a new
// attribute.
func TestUnquotedAttrEscapesEveryWhitespace(t *testing.T) {
	for _, ch := range asciiSpaceSet() {
		ch := ch
		t.Run("byte_0x"+hexByte(ch), func(t *testing.T) {
			value := "1" + string(ch) + "onmouseover=evil"
			out, err := Render("<a c={{x}}>", map[string]string{"x": value})
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			// Inspect only the rendered attribute value: the template
			// itself legitimately contains a space in "<a c=".
			val, ok := strings.CutPrefix(out, "<a c=")
			if !ok {
				t.Fatalf("unexpected output %q", out)
			}
			val = strings.TrimSuffix(val, ">")
			if strings.Contains(val, string(ch)) {
				t.Fatalf("whitespace byte 0x%02x leaked raw into %q; "+
					"it can end the unquoted value and inject an attribute", ch, out)
			}
			want := "&#" + itoa(int(ch)) + ";"
			if !strings.Contains(out, want) {
				t.Fatalf("output %q missing escape %q for byte 0x%02x", out, want, ch)
			}
		})
	}
}

// TestUnquotedWhitespaceParity asserts that the set of bytes the unquoted
// escaper treats as whitespace equals the set isASCIISpace recognizes — the
// two definitions must never drift apart again.
func TestUnquotedWhitespaceParity(t *testing.T) {
	esc := escaperFor(ctxAttrUnquoted)
	for b := 0; b < 256; b++ {
		ch := byte(b)
		_, escaped := esc[ch]
		_, punct := unquotedPunctuationEscaper[ch]
		escapedAsSpace := escaped && !punct
		if escapedAsSpace != isASCIISpace(ch) {
			t.Errorf("byte 0x%02x: isASCIISpace=%v but escaped-as-whitespace=%v",
				ch, isASCIISpace(ch), escapedAsSpace)
		}
	}
}

// TestUnquotedAttrKeepsNormalValues ensures ordinary unquoted values still
// render verbatim after the whitespace fix.
func TestUnquotedAttrKeepsNormalValues(t *testing.T) {
	out, err := Render("<a c={{x}}>", map[string]string{"x": "abc-123_x"})
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if want := "<a c=abc-123_x>"; out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// hexByte formats a byte as two uppercase hex digits (test helper).
func hexByte(ch byte) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[ch>>4], digits[ch&0xf]})
}
