package ctxtmpl

import "testing"

// TestUnquotedAttrEscapesEveryScannerWhitespace is one case per byte the
// scanner treats as whitespace: each one must be entity-encoded inside an
// unquoted attribute so it cannot terminate the value and inject a new
// attribute name. Before the fix the cases for '\f' and '\v' failed because
// those bytes reached the output raw.
func TestUnquotedAttrEscapesEveryScannerWhitespace(t *testing.T) {
	for _, ch := range asciiSpaceBytes {
		ch := ch
		t.Run("byte_0x"+hexByte(ch), func(t *testing.T) {
			value := "abc" + string(ch) + "onload=x"
			got, err := Render(`<a c={{x}}>`, map[string]string{"x": value})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			want := "<a c=abc" + asciiSpaceEntities[ch] + "onload&#61;x>"
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// TestWhitespaceSetsAgree asserts directly that the scanner's notion of
// whitespace (asciiSpaceBytes, consulted wherever an unquoted value may end)
// and the unquoted-attribute escaper's notion (asciiSpaceEntities) are the
// same set, and that escape behavior matches for every possible byte. Any
// future change to either side fails here instead of reopening the
// attribute-injection gap.
func TestWhitespaceSetsAgree(t *testing.T) {
	scannerSet := make(map[byte]bool, len(asciiSpaceBytes))
	for _, ch := range asciiSpaceBytes {
		scannerSet[ch] = true
	}

	escaperSet := make(map[byte]bool, len(asciiSpaceEntities))
	for ch := range asciiSpaceEntities {
		escaperSet[ch] = true
	}

	if len(scannerSet) != len(escaperSet) {
		t.Fatalf("whitespace set sizes differ: scanner=%v escaper=%v",
			scannerSet, escaperSet)
	}
	for ch := range scannerSet {
		if !escaperSet[ch] {
			t.Fatalf("scanner whitespace 0x%02x is not escaped unquoted", ch)
		}
	}
	for ch := range escaperSet {
		if !scannerSet[ch] {
			t.Fatalf("escaped whitespace 0x%02x is not scanner whitespace", ch)
		}
	}

	spaceEntities := make(map[string]bool, len(asciiSpaceEntities))
	for _, entity := range asciiSpaceEntities {
		spaceEntities[entity] = true
	}

	table := escaperFor(ctxAttrUnquoted)
	for n := 0; n < 256; n++ {
		ch := byte(n)
		got := escapeValue(string(ch), context{kind: ctxAttrUnquoted})
		switch scannerSet[ch] {
		case true:
			if got != asciiSpaceEntities[ch] {
				t.Fatalf("whitespace 0x%02x escaped as %q, want %q",
					ch, got, asciiSpaceEntities[ch])
			}
			if table[ch] != asciiSpaceEntities[ch] {
				t.Fatalf("escape table mismatch for 0x%02x: %q", ch, table[ch])
			}
		case false:
			if spaceEntities[got] {
				t.Fatalf("non-whitespace 0x%02x encoded as whitespace entity %q", ch, got)
			}
		}
	}
}

// TestUnquotedAttrNormalValueUnchanged ensures the fix did not degrade into
// escaping every non-alphanumeric byte: ordinary value characters pass raw.
func TestUnquotedAttrNormalValueUnchanged(t *testing.T) {
	got, err := Render(`<a c={{x}}>`, map[string]string{"x": "abc-123_x"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "<a c=abc-123_x>"; got != want {
		t.Fatalf("normal value altered: got %q want %q", got, want)
	}
}

func hexByte(ch byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[ch>>4], digits[ch&0x0f]})
}
