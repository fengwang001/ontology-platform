package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/lex"
	"ontology/words"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var checks []check
	add := func(name string, ok bool) { checks = append(checks, check{name, ok}) }
	eq := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	add("single-quote backslash is literal",
		mustWords(`'a\b'`, eq) == `a\b` && mustWords(`'a\'`, eq) == `a\`)
	add("double-quote five escapes and literal backslash",
		mustWords(`"a\"b"`, eq) == `a"b` && mustWords(`"a\\b"`, eq) == `a\b` &&
			mustWords(`"a\b"`, eq) == `a\b` && mustWords(`"a\$"`, eq) == `a$` &&
			mustWords("\"a\\\nb\"", eq) == "ab")
	add("unquoted escapes and line continuation",
		mustWords(`a\ b`, eq) == "a b" && mustWords(`a\\`, eq) == `a\` &&
			mustWords("a\\\nb", eq) == "ab")
	add("concatenation and empty words",
		mustWords(`a"b"'c'`, eq) == "abc" &&
			eq(mustSplit(`a '' b`), []string{"a", "", "b"}) &&
			eq(mustSplit(`""''`), []string{""}) && eq(mustSplit(`a""`), []string{"a"}) &&
			eq(mustSplit("\\\n"), []string{}))
	add("three distinguishable errors with offsets", errCheck(`'x`, lex.ErrUnclosedSingle, 0) &&
		errCheck(`"x`, lex.ErrUnclosedDouble, 0) && errCheck(`x\`, lex.ErrTrailingBackslash, 1))
	add("identical result at every chunk boundary", allSplitsConsistent(eq))
	add("roundtrip Split(Quote(w)) == w", roundtripOK(eq))
	add("processed byte counter equals input length", counterOK())
	fail := 0
	for _, c := range checks {
		status := "OK  "
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", len(checks)-fail, len(checks))
	if fail > 0 {
		panic("checks failed")
	}
}

func mustSplit(s string) []string {
	w, err := words.Split(s)
	if err != nil {
		panic(err)
	}
	return w
}

func mustWords(s string, _ func([]string, []string) bool) string {
	return strings.Join(mustSplit(s), "\x00")
}

func errCheck(s string, want error, off int64) bool {
	_, err := words.Split(s)
	var oe *lex.OffsetError
	return errors.As(err, &oe) && errors.Is(err, want) && oe.Offset == off
}

func feedChunks(s string, cut int) ([]string, error) {
	t := words.NewSplitter()
	for i := 0; i < len(s); i += cut {
		end := i + cut
		if end > len(s) {
			end = len(s)
		}
		if err := t.Feed([]byte(s[i:end])); err != nil {
			return nil, err
		}
	}
	err := t.Close()
	return t.Words(), err
}

func allSplitsConsistent(eq func([]string, []string) bool) bool {
	inputs := []string{`a"b"'c' "a\b" a\ b`, "a\\\nb\t'x y'", `"\$"\` + "\n"}
	for _, in := range inputs {
		ref, refErr := feedChunks(in, len(in)+1)
		for cut := 1; cut <= len(in); cut++ {
			got, err := feedChunks(in, cut)
			if (err != nil) != (refErr != nil) || !eq(got, ref) {
				return false
			}
		}
	}
	return true
}

func roundtripOK(eq func([]string, []string) bool) bool {
	cases := [][]string{
		{""}, {"a", "", "b"}, {"a b"}, {"'q'", `"d"`, `\b`, "$x", "a`b"},
		{"line1\nline2"}, {"世界 café"}, {"a'b\"c\\d"},
	}
	for _, w := range cases {
		got, err := words.Split(words.Quote(w))
		if err != nil || !eq(got, w) {
			return false
		}
	}
	return true
}

func counterOK() bool {
	in := strings.Repeat(`a'b"\$\ x\`+"\n", 40000)
	m := lex.New(func(lex.Event) {})
	for i := 0; i < len(in); i++ {
		if err := m.Feed([]byte(in[i : i+1])); err != nil {
			return false
		}
	}
	return m.Bytes() == int64(len(in))
}
