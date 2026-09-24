package words_test

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/lex"
	"ontology/words"
)

func TestSplitSemantics(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`'a\b'`, []string{`a\b`}},
		{`'a\'`, []string{`a\`}},
		{`"a\"b"`, []string{`a"b`}},
		{`"a\\b"`, []string{`a\b`}},
		{`"a\b"`, []string{`a\b`}},
		{`"a\$"`, []string{`a$`}},
		{"\"a\\\nb\"", []string{"ab"}},
		{"\"a\\\t\"", []string{"a\\\t"}},
		{`a\ b`, []string{"a b"}},
		{`a\\`, []string{`a\`}},
		{"a\\\nb", []string{"ab"}},
		{`a"b"'c'`, []string{"abc"}},
		{`a '' b`, []string{"a", "", "b"}},
		{`""''`, []string{""}},
		{`a""`, []string{"a"}},
		{"\\\n", nil},
		{"  \t\n ", nil},
		{"a  b\tc\nd", []string{"a", "b", "c", "d"}},
		{"`echo $x`", []string{"`echo", "$x`"}},
		{"'é字'", []string{"é字"}},
	}
	for _, c := range cases {
		got, err := words.Split(c.in)
		if err != nil {
			t.Errorf("Split(%q) error: %v", c.in, err)
		} else if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		in     string
		kind   lex.Kind
		offset int
	}{
		{`ab 'cd`, lex.KindUnclosedSingle, 3},
		{"'a\nb", lex.KindUnclosedSingle, 0},
		{`x "yz`, lex.KindUnclosedDouble, 2},
		{`"a\`, lex.KindUnclosedDouble, 0},
		{`ab\`, lex.KindTrailingBackslash, 2},
		{"'a' \"b", lex.KindUnclosedDouble, 4},
	}
	for _, c := range cases {
		_, err := words.Split(c.in)
		le, ok := err.(*lex.Error)
		if !ok {
			t.Errorf("Split(%q) err = %v, want *lex.Error", c.in, err)
		} else if le.Kind != c.kind || le.Offset != c.offset {
			t.Errorf("Split(%q) = {kind:%d off:%d}, want {%d %d}",
				c.in, le.Kind, le.Offset, c.kind, c.offset)
		}
	}
}

func sameErr(a, b error) bool {
	ae, aok := a.(*lex.Error)
	be, bok := b.(*lex.Error)
	return aok == bok && (!aok || *ae == *be)
}

func TestSplitPoints(t *testing.T) {
	inputs := []string{
		`a\ b"c"$'d'`, "a\\\nb", "\"a\\\nb\"", `""''`, `x "y\`,
		"'a\nb", `ab\`, "a 'b c' \\$d ", "\\\n", `"a\b" 'c\'`,
	}
	for _, in := range inputs {
		want, werr := words.Split(in)
		for i := 0; i <= len(in); i++ {
			sp := words.NewSplitter()
			sp.Feed(in[:i])
			sp.Feed(in[i:])
			gerr := sp.Close()
			if !sameErr(gerr, werr) || (werr == nil && !reflect.DeepEqual(sp.Words(), want)) {
				t.Fatalf("split %q at %d: got %q,%v want %q,%v", in, i, sp.Words(), gerr, want, werr)
			}
		}
		sp := words.NewSplitter()
		for i := 0; i < len(in); i++ {
			sp.Feed(in[i : i+1])
		}
		if gerr := sp.Close(); !sameErr(gerr, werr) || (werr == nil && !reflect.DeepEqual(sp.Words(), want)) {
			t.Fatalf("1-byte feed of %q: got %q,%v want %q,%v", in, sp.Words(), gerr, want, werr)
		}
	}
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

func TestQuoteRoundTrip(t *testing.T) {
	fixed := [][]string{
		nil, {""}, {"a b", ""}, {"'", `"`, `\`, "$", "`", "\n", "\t"},
		{"a'b\"c\\d$e\nf", "é字 x", "'"}, {"simple"},
	}
	r := rand.New(rand.NewSource(1))
	alpha := []rune("ab ' \" \\ $` \n\té字")
	for i := 0; i < 400; i++ {
		w := make([]string, r.Intn(5))
		for j := range w {
			rs := make([]rune, r.Intn(9))
			for k := range rs {
				rs[k] = alpha[r.Intn(len(alpha))]
			}
			w[j] = string(rs)
		}
		fixed = append(fixed, w)
	}
	for _, w := range fixed {
		got, err := words.Split(words.Quote(w))
		if err != nil || !equalWords(got, w) {
			t.Fatalf("round trip of %#v: got %#v, err %v", w, got, err)
		}
	}
}
