package words

import (
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/lex"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"single keeps backslash", `'a\b'`, []string{`a\b`}},
		{"single ends at quote", `'a\'`, []string{`a\`}},
		{"double escaped quote", `"a\"b"`, []string{`a"b`}},
		{"double escaped backslash", `"a\\b"`, []string{`a\b`}},
		{"double keeps other backslash", `"a\b"`, []string{`a\b`}},
		{"double escaped dollar", `"a\$"`, []string{`a$`}},
		{"double escaped backtick", "\"a\\`\"", []string{"a`"}},
		{"double continuation", "\"a\\\nb\"", []string{"ab"}},
		{"unquoted escaped space", `a\ b`, []string{"a b"}},
		{"unquoted escaped backslash", `a\\`, []string{`a\`}},
		{"unquoted continuation", "a\\\nb", []string{"ab"}},
		{"concat pieces", `a"b"'c'`, []string{"abc"}},
		{"empty quoted between spaces", `a '' b`, []string{"a", "", "b"}},
		{"adjacent empty quotes", `""''`, []string{""}},
		{"empty appended", `a""`, []string{"a"}},
		{"bare continuation yields none", "\\\n", nil},
		{"only spaces", "   \t\n", nil},
		{"non-ascii", `"héllo" wörld`, []string{"héllo", "wörld"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Split(c.input)
			if err != nil || !eqWords(got, c.want) {
				t.Fatalf("Split(%q) = %#v, %v; want %#v", c.input, got, err, c.want)
			}
		})
	}
}

func TestSplitErrors(t *testing.T) {
	cases := []struct {
		input  string
		sent   error
		offset int
	}{
		{`'abc`, lex.ErrUnclosedSingle, 0},
		{`a 'b' 'x`, lex.ErrUnclosedSingle, 6},
		{`"abc`, lex.ErrUnclosedDouble, 0},
		{`a"b\"x`, lex.ErrUnclosedDouble, 1},
		{`abc\`, lex.ErrTrailingBackslash, 3},
		{`"abc\`, lex.ErrUnclosedDouble, 0},
	}
	for _, c := range cases {
		_, err := Split(c.input)
		var le *lex.Error
		if !errors.As(err, &le) || !errors.Is(err, c.sent) || le.Offset != c.offset {
			t.Errorf("Split(%q): got %v, want %v at %d", c.input, err, c.sent, c.offset)
		}
	}
}

func TestAllSplitPoints(t *testing.T) {
	inputs := []string{
		`a"b"'c'  "x\$y" \  `,
		"\"a\\\nb\" 'a\\b' c\\\nd",
		`"\$"` + "`" + ` \\ '' ""`,
		`'unclosed`,
		`"unclosed\`,
		`tail\`,
	}
	for _, in := range inputs {
		base, baseErr := Split(in)
		for cut := 0; cut <= len(in); cut++ {
			var s Splitter
			s.Feed(in[:cut])
			s.Feed(in[cut:])
			err := s.Close()
			if !sameErr(err, baseErr) || !reflect.DeepEqual(s.Words(), base) {
				t.Fatalf("cut %d of %q: got %#v/%v want %#v/%v", cut, in, s.Words(), err, base, baseErr)
			}
		}
	}
}

func sameErr(got, want error) bool {
	if want == nil {
		return got == nil
	}
	var g, w *lex.Error
	return errors.As(got, &g) && errors.As(want, &w) &&
		errors.Is(got, w.Err) && g.Offset == w.Offset
}

func eqWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestQuoteRoundtrip(t *testing.T) {
	manual := [][]string{
		nil,
		{""},
		{"", "", ""},
		{"a b", "'", `"`, `\`, "$x", "a`b", "line\nbreak", "tab\there", "héllo"},
		{`it's`, `say "hi"`, `a\b\c`, " ' \" \\ "},
	}
	for _, w := range manual {
		got, err := Split(Quote(w))
		if err != nil || !eqWords(got, w) {
			t.Fatalf("roundtrip %#v: got %#v, %v", w, got, err)
		}
	}

	r := rand.New(rand.NewSource(1))
	alpha := []byte("ab '\"\\$`\n\té")
	for iter := 0; iter < 500; iter++ {
		w := make([]string, 1+r.Intn(6))
		for i := range w {
			b := make([]byte, r.Intn(12))
			for j := range b {
				b[j] = alpha[r.Intn(len(alpha))]
			}
			w[i] = string(b)
		}
		got, err := Split(Quote(w))
		if err != nil || !eqWords(got, w) {
			t.Fatalf("random roundtrip %q: %#v, %v vs %#v", Quote(w), got, err, w)
		}
	}
}

func TestStreamCounter(t *testing.T) {
	in := strings.Repeat(`"a\b"'c'\ `, 1024*1024/10)
	var s Splitter
	for i := 0; i < len(in); i++ {
		s.Feed(in[i : i+1])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Processed() != len(in) {
		t.Fatalf("processed = %d, want %d", s.Processed(), len(in))
	}
}
