package jstr_test

import (
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/jstr"
)

type tc struct {
	name, lit, want string
	kind, off       int
}

var decodeCases = []tc{
	{"empty", `""`, "", -1, 0},
	{"simple", `"abc"`, "abc", -1, 0},
	{"escapes", `"a\"b\\c\/d"`, `a"b\c/d`, -1, 0},
	{"short", `"\b\f\n\r\t"`, "\b\f\n\r\t", -1, 0},
	{"grin raw", `"😀"`, "😀", -1, 0},
	{"pair", `"\uD83D\uDE00"`, "😀", -1, 0},
	{"hex", `"\u0041\u00e9"`, "Aé", -1, 0}, {"nul", `"\u0000"`, "\x00", -1, 0},
	{"del", "\"\x7f\"", "\x7f", -1, 0}, {"2028", "\"\u2028\"", "\u2028", -1, 0},
	{"control", "\"a\x00b\"", "", jstr.KindControl, 2},
	{"unknown x", `"\x"`, "", jstr.KindEscape, 1},
	{"unknown quote", `"\'"`, "", jstr.KindEscape, 1},
	{"short hex", `"\u12"`, "", jstr.KindUnicode, 1},
	{"bad hex", `"\u12GZ"`, "", jstr.KindUnicode, 1},
	{"unterminated", `"abc`, "", jstr.KindUnterminated, 4},
	{"no open", `abc"`, "", jstr.KindUnterminated, 0},
	{"trailing", `"a"x`, "", jstr.KindTrailing, 3},
	{"bad utf8", "\"a\xffb\"", "", jstr.KindUTF8, 2},
	{"lone high", `"\uD83D"`, "", jstr.KindUnicode, 1},
	{"lone low", `"\uDE00"`, "", jstr.KindUnicode, 1}, {"high x", `"\uD83Dx"`, "", jstr.KindUnicode, 1},
	{"high A", `"\uD83DA"`, "", jstr.KindUnicode, 1},
	{"reversed", `"\uDE00\uD83D"`, "", jstr.KindUnicode, 1},
	{"high simple", `"\uD83D\n"`, "", jstr.KindUnicode, 1},
}

func TestDecode(t *testing.T) {
	for _, c := range decodeCases {
		t.Run(c.name, func(t *testing.T) {
			got, err := jstr.Decode([]byte(c.lit))
			if c.kind < 0 {
				if err != nil || got != c.want {
					t.Fatalf("got %q,%v want %q", got, err, c.want)
				}
				var ref string
				if e := json.Unmarshal([]byte(c.lit), &ref); e == nil && ref != c.want {
					t.Fatalf("reference %q != %q", ref, c.want)
				}
				return
			}
			var se *jstr.SyntaxError
			if !errors.As(err, &se) || se.Kind != c.kind || se.Offset != c.off {
				t.Fatalf("err=%v want kind=%d off=%d", err, c.kind, c.off)
			}
		})
	}
}
func TestMinimalEncode(t *testing.T) {
	cases := []struct{ in, want string }{
		{`a"b\c`, `"a\"b\\c"`},
		{"\b\f\n\r\t", `"\b\f\n\r\t"`},
		{"\x00\x01\x1f", `"\u0000\u0001\u001f"`},
		{"/\x7f", "\"/\x7f\""},
		{"\u2028\u2029é😀", "\"\u2028\u2029é😀\""},
	}
	for _, c := range cases {
		got, err := jstr.Encode(c.in)
		if err != nil || string(got) != c.want {
			t.Fatalf("%q -> %q,%v want %q", c.in, got, err, c.want)
		}
	}
	if _, err := jstr.Encode("a\xffb"); !errors.Is(err, jstr.ErrInvalidUTF8) {
		t.Fatalf("invalid utf8 err=%v", err)
	}
}
func sameErr(a, b error) bool {
	var x, y *jstr.SyntaxError
	return errors.As(a, &x) && errors.As(b, &y) && x.Kind == y.Kind && x.Offset == y.Offset
}
func TestSplits(t *testing.T) {
	for _, c := range decodeCases {
		base, berr := jstr.Decode([]byte(c.lit))
		for w := 1; w <= len(c.lit)+1; w++ {
			d := jstr.NewDecoder()
			var werr error
			for i := 0; i < len(c.lit) && werr == nil; i += w {
				j := min(i+w, len(c.lit))
				_, werr = d.Write([]byte(c.lit[i:j]))
			}
			got, cerr := d.Close()
			if berr != nil {
				if !sameErr(berr, firstErr(werr, cerr)) {
					t.Fatalf("%q w=%d %v want %v", c.name, w, firstErr(werr, cerr), berr)
				}
			} else if werr != nil || cerr != nil || got != base {
				t.Fatalf("%q w=%d %q %v %v", c.name, w, got, werr, cerr)
			}
		}
	}
}
func firstErr(a, b error) error {
	return map[bool]error{true: a, false: b}[a != nil]
}
func TestChecks(t *testing.T) {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < 50000; i++ {
		sb.WriteString(`\uD83D\uDE00`)
	}
	sb.WriteByte('"')
	lit := sb.String()
	d := jstr.NewDecoder()
	_, werr := d.Write([]byte(lit))
	_, cerr := d.Close()
	if werr != nil || cerr != nil {
		t.Fatalf("%v %v", werr, cerr)
	}
	if c := d.Checks(); c > 2*len(lit) {
		t.Fatalf("checks=%d > 2*%d", c, len(lit))
	}
}
func TestRoundTripRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	runes := []rune("ab\"\\/\t\n\r\x00\x01\x1f\x7fé😀\u2028\U0010FFFF")
	for iter := 0; iter < 300; iter++ {
		var sb strings.Builder
		for i, n := 0, r.Intn(16); i < n; i++ {
			sb.WriteRune(runes[r.Intn(len(runes))])
		}
		s := sb.String()
		enc, err := jstr.Encode(s)
		if err != nil {
			t.Fatal(err)
		}
		got, err := jstr.Decode(enc)
		if err != nil || got != s {
			t.Fatalf("%q -> %q %v", s, got, err)
		}
		re, err := jstr.Encode(got)
		if err != nil || string(re) != string(enc) {
			t.Fatalf("non-canonical %s vs %s", re, enc)
		}
	}
}
