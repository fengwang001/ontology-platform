package jstr_test

import (
	"errors"
	"testing"

	"ontology/jstr"
)

func offOf(err error) int {
	var e *jstr.Error
	if errors.As(err, &e) {
		return e.Off
	}
	return -1
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		lit  string
		want error
		off  int
	}{
		{"\"a\x01\"", jstr.ErrControl, 2},
		{"\"\t\"", jstr.ErrControl, 1},
		{`"\x"`, jstr.ErrEscape, 2},
		{`"\'"`, jstr.ErrEscape, 2},
		{`"\U0041"`, jstr.ErrEscape, 2},
		{`"\u12G4"`, jstr.ErrHex, 5},
		{`"\u12"`, jstr.ErrHex, 5}, // closing quote interrupts the 4 digits
		{`"abc`, jstr.ErrUnterminated, 4},
		{``, jstr.ErrUnterminated, 0},
		{`abc"`, jstr.ErrUnterminated, 0},
		{`"a" `, jstr.ErrTrailing, 3},
		{`"a""`, jstr.ErrTrailing, 3},
	}
	for _, c := range cases {
		_, err := jstr.Decode([]byte(c.lit))
		if !errors.Is(err, c.want) || offOf(err) != c.off {
			t.Errorf("Decode(%q): err=%v off=%d, want %v off=%d",
				c.lit, err, offOf(err), c.want, c.off)
		}
	}
}

func TestSurrogates(t *testing.T) {
	good := []struct{ lit, want string }{
		{`"\uD83D\uDE00"`, "\U0001F600"},
		{"\"😀\"", "\U0001F600"},
		{`"\uD800\uDC00"`, string(rune(0x10000))},
		{`"\uDBFF\uDFFF"`, string(rune(0x10FFFF))},
		{`"\u0041"`, "A"},
	}
	for _, c := range good {
		if got, err := jstr.Decode([]byte(c.lit)); err != nil || got != c.want {
			t.Errorf("Decode(%s) = %q, %v; want %q", c.lit, got, err, c.want)
		}
	}
	bad := []struct {
		lit string
		off int
	}{
		{`"\uD83D"`, 1},       // lone high surrogate
		{`"\uDE00"`, 1},       // lone low surrogate
		{`"\uD83Dx"`, 1},      // high surrogate then plain char
		{`"\uD83DA"`, 1},      // not "replacement char + A"
		{`"\uDE00\uD83D"`, 1}, // reversed pair
		{`"\uD83D\u0041"`, 1}, // high then non-surrogate escape
		{`"\uD83D\uD83D"`, 1}, // high then high
		{`"\uD83D\uDE0"`, 12}, // pair's second unit short on hex
	}
	for _, c := range bad {
		want := jstr.ErrSurrogate
		if c.off == 12 {
			want = jstr.ErrHex
		}
		_, err := jstr.Decode([]byte(c.lit))
		if !errors.Is(err, want) || offOf(err) != c.off {
			t.Errorf("Decode(%s): err=%v off=%d, want %v off=%d",
				c.lit, err, offOf(err), want, c.off)
		}
	}
}

func TestInvalidUTF8(t *testing.T) {
	bad := []struct {
		lit []byte
		off int
	}{
		{[]byte{'"', 0xFF, '"'}, 1},                   // invalid lead byte
		{[]byte{'"', 0x80, '"'}, 1},                   // stray continuation
		{[]byte{'"', 0xC0, 0x80, '"'}, 1},             // overlong NUL
		{[]byte{'"', 0xC3, '"'}, 2},                   // truncated 2-byte char
		{[]byte{'"', 0xED, 0xA0, 0x80, '"'}, 2},       // UTF-8 of U+D800
		{[]byte{'"', 0xF4, 0x90, 0x80, 0x80, '"'}, 2}, // above U+10FFFF
	}
	for _, c := range bad {
		if _, err := jstr.Decode(c.lit); !errors.Is(err, jstr.ErrUTF8) || offOf(err) != c.off {
			t.Errorf("Decode(%q): err=%v off=%d, want ErrUTF8 off=%d",
				c.lit, err, offOf(err), c.off)
		}
	}
	for _, s := range []string{"\xff", "a\xc3", "\xed\xa0\x80"} {
		if _, err := jstr.Encode(s); !errors.Is(err, jstr.ErrUTF8) {
			t.Errorf("Encode(%q) should fail with ErrUTF8, got %v", s, err)
		}
	}
}

func TestEncodeMinimal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", `""`},
		{`"`, `"\""`},
		{`\`, `"\\"`},
		{"/", `"/"`},                             // slash never escaped
		{"\x7f", "\"\x7f\""},                     // DEL raw
		{"\u2028\u2029", "\"\u2028\u2029\""},     // line separators raw
		{"\b\f\n\r\t", `"\b\f\n\r\t"`},           // short forms
		{"\x00\x01\x1f", `"\u0000\u0001\u001f"`}, // \u00XX lowercase
		{"é世界\U0001F600", "\"é世界\U0001F600\""}, // non-ASCII raw
	}
	for _, c := range cases {
		if got, err := jstr.Encode(c.in); err != nil || string(got) != c.want {
			t.Errorf("Encode(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}
