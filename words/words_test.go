package words

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/lex"
)

func TestSplitTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"only spaces", "  \t\n ", nil},
		{"simple", "a b", []string{"a", "b"}},
		{"single literal bs", `'a\b'`, []string{`a\b`}},
		{"single ends at quote", `'a\'`, []string{`a\`}},
		{"double esc quote", `"a\"b"`, []string{`a"b`}},
		{"double esc bs", `"a\\b"`, []string{`a\b`}},
		{"double keep bs", `"a\b"`, []string{`a\b`}},
		{"double esc dollar", `"a\$"`, []string{`a$`}},
		{"double esc backtick", "\"a\\`\"", []string{"a`"}},
		{"double continuation", "\"a\\\nb\"", []string{"ab"}},
		{"unquoted esc space", `a\ b`, []string{"a b"}},
		{"unquoted esc bs", `a\\`, []string{`a\`}},
		{"unquoted continuation", "a\\\nb", []string{"ab"}},
		{"concat mixed", `a"b"'c'`, []string{"abc"}},
		{"empty quoted word", "a '' b", []string{"a", "", "b"}},
		{"two empty quotes", `""''`, []string{""}},
		{"suffix empty quote", `a""`, []string{"a"}},
		{"lone continuation", "\\\n", nil},
		{"utf8", "世界 café", []string{"世界", "café"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Split(tc.in)
			if err != nil || !equalWords(got, tc.want) {
				t.Fatalf("Split(%q) = %#v, %v; want %#v", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestErrorsTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
		off  int64
	}{
		{"single", `a 'x`, lex.ErrUnclosedSingle, 2},
		{"double", `a "x`, lex.ErrUnclosedDouble, 2},
		{"double bs", `"a\`, lex.ErrUnclosedDouble, 0},
		{"trailing bs", `ab\`, lex.ErrTrailingBackslash, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Split(tc.in)
			var oe *lex.OffsetError
			if !errors.As(err, &oe) || !errors.Is(err, tc.want) || oe.Offset != tc.off {
				t.Fatalf("Split(%q) err = %v, want %v at %d", tc.in, err, tc.want, tc.off)
			}
		})
	}
}

func TestEveryChunkBoundary(t *testing.T) {
	inputs := []string{
		`a"b"'c' "a\b" a\ b`,
		"a\\\nb\t'x y' \"\\$\\\"\"",
		"plain text\n'tab\there' \"q\\`q\"",
	}
	for _, in := range inputs {
		refW, refErr := feedBy(in, len(in)+1)
		for cut := 1; cut <= len(in); cut++ {
			got, err := feedBy(in, cut)
			if (err == nil) != (refErr == nil) || !equalWords(got, refW) {
				t.Fatalf("input %q cut %d: %#v %v; ref %#v %v", in, cut, got, err, refW, refErr)
			}
		}
	}
}

func TestQuoteRoundtripRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	alphabet := []rune("abc AB'\"\\$`\n\t世界é")
	for iter := 0; iter < 2000; iter++ {
		n := r.Intn(6)
		w := make([]string, n)
		for i := range w {
			var b strings.Builder
			for j, k := 0, r.Intn(8); j < k; j++ {
				b.WriteRune(alphabet[r.Intn(len(alphabet))])
			}
			w[i] = b.String()
		}
		got, err := Split(Quote(w))
		if err != nil || !equalWords(got, w) {
			t.Fatalf("roundtrip %#v: got %#v, %v", w, got, err)
		}
	}
}

func feedBy(in string, cut int) ([]string, error) {
	s := NewSplitter()
	for i := 0; i < len(in); i += cut {
		end := i + cut
		if end > len(in) {
			end = len(in)
		}
		if err := s.Feed([]byte(in[i:end])); err != nil {
			return nil, err
		}
	}
	return s.Words(), s.Close()
}

func equalWords(a, b []string) bool {
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
