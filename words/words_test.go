package words_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/lex"
	"ontology/words"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"sq backslash literal", `'a\b'`, []string{`a\b`}},
		{"sq ends at second quote", `'a\'`, []string{`a\`}},
		{"dq escaped quote", `"a\"b"`, []string{`a"b`}},
		{"dq escaped backslash", `"a\\b"`, []string{`a\b`}},
		{"dq kept backslash", `"a\b"`, []string{`a\b`}},
		{"dq escaped dollar", `"a\$"`, []string{`a$`}},
		{"dq escaped backtick", "\"a\\`b\"", []string{"a`b"}},
		{"dq continuation", "\"a\\\nb\"", []string{"ab"}},
		{"unquoted escaped space", `a\ b`, []string{"a b"}},
		{"unquoted escaped backslash", `a\\`, []string{`a\`}},
		{"unquoted continuation", "a\\\nb", []string{"ab"}},
		{"concat quoted parts", `a"b"'c'`, []string{"abc"}},
		{"empty word between", `a '' b`, []string{"a", "", "b"}},
		{"two empty quotes one word", `""''`, []string{""}},
		{"trailing empty quote", `a""`, []string{"a"}},
		{"continuation only no words", "\\\n", nil},
		{"blank separated", " a  b\tc ", []string{"a", "b", "c"}},
		{"newline in single quotes", "'a\nb'", []string{"a\nb"}},
		{"non ascii", `'中'"\$"`, []string{"中$"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := words.Split(tc.in)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Split(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestSplitErrors(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		sentinel error
		offset   int
	}{
		{"unterminated single", `'abc`, words.ErrUnterminatedSingle, 0},
		{"single offset", `ab'cd`, words.ErrUnterminatedSingle, 2},
		{"unterminated double", `"abc`, words.ErrUnterminatedDouble, 0},
		{"double offset", `ab"cd`, words.ErrUnterminatedDouble, 2},
		{"double ends in backslash", `"a\`, words.ErrUnterminatedDouble, 0},
		{"trailing backslash", `a\`, words.ErrTrailingBackslash, 1},
		{"lone backslash", `\`, words.ErrTrailingBackslash, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := words.Split(tc.in)
			var le *lex.Error
			if !errors.As(err, &le) || !errors.Is(err, tc.sentinel) {
				t.Fatalf("err = %v, want %v", err, tc.sentinel)
			}
			if le.Offset != tc.offset {
				t.Fatalf("offset = %d, want %d", le.Offset, tc.offset)
			}
		})
	}
}

// TestAllSplitPoints feeds each input at every 2-way split point and
// byte by byte; words and errors must match a one-shot Split.
func TestAllSplitPoints(t *testing.T) {
	inputs := []string{
		`a"b"'c' 'd\$' e\ f "g\h" i"" ''j`,
		"a\\\nb \"c\\\nd\" 'e\nf' \\ \\\\\\",
		"\"a\\", "'a", "a\\", "\\\n", "", "中'x'",
	}
	for _, in := range inputs {
		want, wantErr := words.Split(in)
		for i := 0; i <= len(in)+1; i++ {
			var sp words.Splitter
			if i <= len(in) {
				sp.Feed(in[:i])
				sp.Feed(in[i:])
			} else {
				for j := 0; j < len(in); j++ {
					sp.Feed(in[j : j+1])
				}
			}
			err := sp.Close()
			if fmt.Sprint(err) != fmt.Sprint(wantErr) {
				t.Fatalf("%q cut %d: err %v, want %v", in, i, err, wantErr)
			}
			if wantErr == nil && !reflect.DeepEqual(sp.Words(), want) {
				t.Fatalf("%q cut %d: %q, want %q", in, i, sp.Words(), want)
			}
		}
	}
}

func TestQuote(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty word", []string{""}, `''`},
		{"plain", []string{"a", "b"}, `'a' 'b'`},
		{"single quote inside", []string{"a'b"}, `'a'\''b'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := words.Quote(tc.in); got != tc.want {
				t.Fatalf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRoundTrip checks Split(Quote(w)) == w for random word lists.
func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("ab \t\n'\"\\$`é中")
	for iter := 0; iter < 500; iter++ {
		ws := make([]string, rng.Intn(5))
		for i := range ws {
			r := make([]rune, rng.Intn(8))
			for j := range r {
				r[j] = alphabet[rng.Intn(len(alphabet))]
			}
			ws[i] = string(r)
		}
		got, err := words.Split(words.Quote(ws))
		if len(ws) == 0 {
			ws = nil
		}
		if err != nil || !reflect.DeepEqual(got, ws) {
			t.Fatalf("iter %d: got %q, %v; want %q", iter, got, err, ws)
		}
	}
}
