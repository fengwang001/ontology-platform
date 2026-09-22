package tokenize

import (
	"errors"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []Token
	}{
		{"empty", "", nil},
		{"only spaces", "   \t\n", nil},
		{"simple lower fold", "Hello World", []Token{{"hello", 0}, {"world", 1}}},
		{"digits join", "abc123 v2", []Token{{"abc123", 0}, {"v2", 1}}},
		{"sample positions", "x a b c y",
			[]Token{{"x", 0}, {"a", 1}, {"b", 2}, {"c", 3}, {"y", 4}}},
		{"phrase at start", "a b c", []Token{{"a", 0}, {"b", 1}, {"c", 2}}},
		{"reordered fails adjacency", "x a c b y",
			[]Token{{"x", 0}, {"a", 1}, {"c", 2}, {"b", 3}, {"y", 4}}},
		{"hyphen inside", "state-of-the-art", []Token{{"state-of-the-art", 0}}},
		{"underscore inside", "snake_case_id", []Token{{"snake_case_id", 0}}},
		{"leading hyphen splits", "-abc x-y-", []Token{{"abc", 0}, {"x-y", 1}}},
		{"dot splits", "v1.2 a.b.", []Token{{"v1", 0}, {"2", 1}, {"a", 2}, {"b", 3}}},
		{"punct splits", "a,b;c!d?", []Token{{"a", 0}, {"b", 1}, {"c", 2}, {"d", 3}}},
		{"cjk unigram", "你好abc", []Token{{"你", 0}, {"好", 1}, {"abc", 2}}},
		{"multi spaces one boundary", "a    b", []Token{{"a", 0}, {"b", 1}}},
		{"newline boundary", "a\nb", []Token{{"a", 0}, {"b", 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Tokenize(tc.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("token %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestTokenizeErrors(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"term too long ascii", strings.Repeat("a", MaxTermRunes+1)},
		{"term too long after prefix", "x " + strings.Repeat("y", MaxTermRunes+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Tokenize(tc.text); !errors.Is(err, ErrTermTooLong) {
				t.Fatalf("got %v, want ErrTermTooLong", err)
			}
		})
	}
}
