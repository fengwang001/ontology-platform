package words

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/lex"
)

func TestSingleQuote(t *testing.T) {
	cases := []struct {
		in  string
		out []string
	}{
		{`'a\b'`, []string{`a\b`}},
		{`'a\'`, []string{`a\`}},
		{`'it''s'`, []string{`its`}},
		{`a 'b c' d`, []string{"a", "b c", "d"}},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if err != nil || !reflect.DeepEqual(got, c.out) {
			t.Errorf("Split(%q) = %q, %v; want %q", c.in, got, err, c.out)
		}
	}
}

func TestDoubleQuote(t *testing.T) {
	nl := "\n"
	cases := []struct {
		in  string
		out []string
	}{
		{`"a\"b"`, []string{`a"b`}},
		{`"a\\b"`, []string{`a\b`}},
		{`"a\b"`, []string{`a\b`}},
		{`"a\$"`, []string{`a$`}},
		{`"a\` + "`" + `b"`, []string{"a`b"}},
		{`"a\` + nl + `b"`, []string{"ab"}},
		{`"a\ b"`, []string{`a\ b`}},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if err != nil || !reflect.DeepEqual(got, c.out) {
			t.Errorf("Split(%q) = %q, %v; want %q", c.in, got, err, c.out)
		}
	}
}

func TestUnquotedEscape(t *testing.T) {
	cases := []struct {
		in  string
		out []string
	}{
		{`a\ b`, []string{"a b"}},
		{`a\\`, []string{`a\`}},
		{"a\\\nb", []string{"ab"}},
		{`a\"b`, []string{`a"b`}},
		{`\  x`, []string{" ", "x"}},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if err != nil || !reflect.DeepEqual(got, c.out) {
			t.Errorf("Split(%q) = %q, %v; want %q", c.in, got, err, c.out)
		}
	}
}

func TestJoiningAndEmptyWords(t *testing.T) {
	cases := []struct {
		in  string
		out []string
	}{
		{`a"b"'c'`, []string{"abc"}},
		{`a '' b`, []string{"a", "", "b"}},
		{`""''`, []string{""}},
		{`a""`, []string{"a"}},
		{"\\\n", []string{}},
		{"   ", []string{}},
		{`"" a ""`, []string{"", "a", ""}},
		{`''""x'y'`, []string{"xy"}},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if err != nil || !reflect.DeepEqual(got, c.out) {
			t.Errorf("Split(%q) = %q, %v; want %q", c.in, got, err, c.out)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		in   string
		want error
		off  int
	}{
		{`abc 'def`, ErrUnterminatedSingleQuote, 4},
		{`abc "def`, ErrUnterminatedDoubleQuote, 4},
		{`"abc\"`, ErrUnterminatedDoubleQuote, 0},
		{`abc\`, ErrTrailingBackslash, 3},
		{`"a\`, ErrTrailingBackslash, 2},
		{`''\'''`, nil, 0},
	}
	for _, c := range cases {
		got, err := Split(c.in)
		if c.want == nil {
			if err != nil {
				t.Errorf("Split(%q) unexpected error %v", c.in, err)
			}
			continue
		}
		var le *LexError
		if !errors.As(err, &le) || !errors.Is(err, c.want) {
			t.Errorf("Split(%q) err = %v; want %v", c.in, err, c.want)
			continue
		}
		if le.Offset != c.off {
			t.Errorf("Split(%q) offset = %d; want %d", c.in, le.Offset, c.off)
		}
		if got != nil {
			t.Errorf("Split(%q) words = %q; want nil on error", c.in, got)
		}
	}
}

func TestChunking(t *testing.T) {
	inputs := []string{
		`a 'b c' "d\"e" f\ g`,
		"x\\\ny  ''  \"$x\"",
		`'unterminated`,
		`abc\`,
		`"a\`,
		`'"''\''"\""\\`,
		"",
		"   plain   text  ",
	}
	for _, in := range inputs {
		var refSP Splitter
		refSP.Feed(in)
		refErr := refSP.Close()
		ref := refSP.Words()
		// Every cut position 0..len(in), feeding two fragments.
		for cut := 0; cut <= len(in); cut++ {
			var sp Splitter
			sp.Feed(in[:cut])
			sp.Feed(in[cut:])
			err := sp.Close()
			if !sameErr(err, refErr) || !reflect.DeepEqual(sp.Words(), ref) {
				t.Fatalf("chunk mismatch at cut %d for %q:\n got %q %v\nwant %q %v",
					cut, in, sp.Words(), err, ref, refErr)
			}
		}
		// One byte at a time.
		var sp Splitter
		for i := 0; i < len(in); i++ {
			sp.Feed(in[i : i+1])
		}
		err := sp.Close()
		if !sameErr(err, refErr) || !reflect.DeepEqual(sp.Words(), ref) {
			t.Fatalf("1-byte chunking mismatch for %q: %q %v; want %q %v",
				in, sp.Words(), err, ref, refErr)
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	var la, lb *LexError
	if !errors.As(a, &la) || !errors.As(b, &lb) {
		return false
	}
	return la.Offset == lb.Offset && errors.Is(la.Err, lb.Err)
}

func TestQuoteRoundTrip(t *testing.T) {
	fixed := [][]string{
		{""},
		{"", "", ""},
		{"a b", "a'b", `a"b`, `a\b`, "$x", "a`b"},
		{"line\nbreak", "tab\there", "café", "中文"},
		{"'\"\\$`"},
		{"plain", "x=y", "a;b", "*"},
	}
	for _, w := range fixed {
		got, err := Split(Quote(w))
		if err != nil || !reflect.DeepEqual(got, w) {
			t.Fatalf("round trip %q -> %q -> %q, err=%v", w, Quote(w), got, err)
		}
	}
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("a'\"\\ $`\n\t;|<>é中")
	for iter := 0; iter < 2000; iter++ {
		nw := rng.Intn(5)
		w := make([]string, nw)
		for i := range w {
			r := make([]rune, rng.Intn(6))
			for j := range r {
				r[j] = alphabet[rng.Intn(len(alphabet))]
			}
			w[i] = string(r)
		}
		got, err := Split(Quote(w))
		if err != nil || !reflect.DeepEqual(got, w) {
			t.Fatalf("round trip %q -> %q -> %q, err=%v", w, Quote(w), got, err)
		}
	}
}

func TestByteCounter(t *testing.T) {
	var lx lex.Lexer
	size := 1 << 20
	in := make([]byte, 0, size)
	for len(in) < size {
		in = append(in, `a 'b c\'d' "e\"f" g\ h`...)
	}
	in = in[:size]
	for _, b := range in {
		lx.Step(b, len(in))
	}
	if lx.Count() != size {
		t.Fatalf("count = %d; want %d", lx.Count(), size)
	}
}
