package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/lex"
	"ontology/words"
)

type check struct {
	name string
	ok   bool
}

func eq(in string, want []string) bool {
	got, err := words.Split(in)
	return err == nil && reflect.DeepEqual(got, want)
}

func errAt(in string, sentinel error, off int) bool {
	_, err := words.Split(in)
	var le *words.LexError
	return errors.As(err, &le) && errors.Is(err, sentinel) && le.Offset == off
}

func chunksOK(in string) bool {
	var ref words.Splitter
	ref.Feed(in)
	wErr := ref.Close()
	want := ref.Words()
	for cut := 0; cut <= len(in); cut++ {
		var sp words.Splitter
		sp.Feed(in[:cut])
		sp.Feed(in[cut:])
		err := sp.Close()
		if !reflect.DeepEqual(sp.Words(), want) ||
			(err == nil) != (wErr == nil) || (err != nil && err.Error() != wErr.Error()) {
			return false
		}
	}
	return true
}

func main() {
	w := []string{"", "a b", "a'b", `a"b`, `a\b`, "$x", "a`b",
		"l\nl", "café", "中文"}

	var lx lex.Lexer
	big := make([]byte, 1<<20)
	for i := range big {
		lx.Step(big[i], i)
	}

	checks := []check{
		{"single-quote literal backslash", eq(`'a\b'`, []string{`a\b`}) && eq(`'a\'`, []string{`a\`})},
		{"double-quote escapes & literal", eq(`"a\"b"`, []string{`a"b`}) &&
			eq(`"a\\b"`, []string{`a\b`}) && eq(`"a\b"`, []string{`a\b`}) &&
			eq(`"a\$"`, []string{`a$`}) && eq("\"a\\\nb\"", []string{"ab"})},
		{"unquoted escape & continuation", eq(`a\ b`, []string{"a b"}) &&
			eq(`a\\`, []string{`a\`}) && eq("a\\\nb", []string{"ab"})},
		{"joining & empty words", eq(`a"b"'c'`, []string{"abc"}) &&
			eq(`a '' b`, []string{"a", "", "b"}) && eq(`""''`, []string{""}) &&
			eq(`a""`, []string{"a"}) && eq("\\\n", []string{})},
		{"three distinct errors w/ offset",
			errAt(`abc 'def`, words.ErrUnterminatedSingleQuote, 4) &&
				errAt(`abc "def`, words.ErrUnterminatedDoubleQuote, 4) &&
				errAt(`abc\`, words.ErrTrailingBackslash, 3)},
		{"all chunk points identical",
			chunksOK(`a 'b c' "d\"e" f\ g`) && chunksOK(`'x`) && chunksOK(`a\`)},
		{"quote round-trip", eq(words.Quote(w), w)},
		{"byte counter == input bytes", lx.Count() == 1<<20},
	}

	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.ok {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}
