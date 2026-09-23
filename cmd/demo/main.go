package main

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"

	"ontology/lex"
	"ontology/words"
)

func splitOK(in string, want []string) bool {
	got, err := words.Split(in)
	return err == nil && reflect.DeepEqual(got, want)
}

func errorsOK() bool {
	cases := []struct {
		in string
		se error
		at int
	}{
		{`'abc`, lex.ErrUnclosedSingle, 0},
		{`a"b`, lex.ErrUnclosedDouble, 1},
		{`abc\`, lex.ErrTrailingBackslash, 3},
	}
	for _, c := range cases {
		_, err := words.Split(c.in)
		var le *lex.Error
		if !errors.As(err, &le) || !errors.Is(err, c.se) || le.Offset != c.at {
			return false
		}
	}
	return true
}

func cutsOK() bool {
	for _, in := range []string{
		`a"b"'c' "x\$y" \ `,
		"\"a\\\nb\" c\\\nd 'unclosed",
		`tail\`,
	} {
		base, baseErr := words.Split(in)
		for cut := 0; cut <= len(in); cut++ {
			var s words.Splitter
			s.Feed(in[:cut])
			s.Feed(in[cut:])
			err := s.Close()
			if !reflect.DeepEqual(s.Words(), base) ||
				(err == nil) != (baseErr == nil) {
				return false
			}
		}
	}
	return true
}

func roundtripOK() bool {
	r := rand.New(rand.NewSource(7))
	alpha := []byte("ab '\"\\$`\n\té")
	for iter := 0; iter < 200; iter++ {
		w := make([]string, 1+r.Intn(5))
		for i := range w {
			b := make([]byte, r.Intn(10))
			for j := range b {
				b[j] = alpha[r.Intn(len(alpha))]
			}
			w[i] = string(b)
		}
		got, err := words.Split(words.Quote(w))
		if err != nil || !reflect.DeepEqual(got, w) {
			return false
		}
	}
	return splitOK(words.Quote([]string{"", " ", "it's", `a"b\c$`}),
		[]string{"", " ", "it's", `a"b\c$`})
}

func counterOK() bool {
	in := strings.Repeat(`"a\b"'c'\ `, 1024*1024/10)
	var s words.Splitter
	for i := 0; i < len(in); i++ {
		s.Feed(in[i : i+1])
	}
	return s.Close() == nil && s.Processed() == len(in)
}

func main() {
	checks := []struct {
		name string
		ok   bool
	}{
		{"single-quote literal backslash", splitOK(`'a\b' 'a\'`, []string{`a\b`, `a\`})},
		{"double-quote five escapes/keep", splitOK(`"a\"b" "a\\b" "a\b" "a\$" "a\`+"`"+`"`,
			[]string{`a"b`, `a\b`, `a\b`, `a$`, "a`"}) &&
			splitOK("\"a\\\nb\"", []string{"ab"})},
		{"unquoted escape and continuation", splitOK(`a\ b a\\`, []string{"a b", `a\`}) &&
			splitOK("a\\\nb", []string{"ab"})},
		{"concat and empty words incl \"\"''", splitOK(`a"b"'c' a '' b ""'' a""`,
			[]string{"abc", "a", "", "b", "", "a"})},
		{"three distinct errors with offsets", errorsOK()},
		{"all split points identical", cutsOK()},
		{"quote roundtrip", roundtripOK()},
		{"processed byte counter", counterOK()},
	}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, fail = "FAIL", fail+1
		}
		fmt.Printf("%s  %s\n", status, c.name)
	}
	fmt.Printf("total: %d/%d OK\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("demo checks failed")
	}
}
