// Command demo prints OK/FAIL checks for the words splitter.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"ontology/lex"
	"ontology/words"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL %s\n", name)
}

func split(s string) []string {
	w, err := words.Split(s)
	if err != nil {
		return nil
	}
	return w
}

func errAt(s string, sentinel error, off int) bool {
	_, err := words.Split(s)
	var le *lex.Error
	return errors.As(err, &le) && errors.Is(err, sentinel) && le.Offset == off
}

// sameChunks verifies every 2-way split point and 1-byte feeding.
func sameChunks(inputs []string) bool {
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
			if (err == nil) != (wantErr == nil) {
				return false
			}
			if wantErr != nil && err.Error() != wantErr.Error() {
				return false
			}
			if wantErr == nil && !reflect.DeepEqual(sp.Words(), want) {
				return false
			}
		}
	}
	return true
}

func main() {
	check("single-quote: backslash is literal",
		reflect.DeepEqual(split(`'a\b'`), []string{`a\b`}) &&
			reflect.DeepEqual(split(`'a\'`), []string{`a\`}))
	check("double-quote: 5 escapes + retention",
		reflect.DeepEqual(split(`"a\"b"`), []string{`a"b`}) &&
			reflect.DeepEqual(split(`"a\\b"`), []string{`a\b`}) &&
			reflect.DeepEqual(split(`"a\b"`), []string{`a\b`}) &&
			reflect.DeepEqual(split(`"a\$"`), []string{`a$`}) &&
			reflect.DeepEqual(split("\"a\\\nb\""), []string{"ab"}))
	check("unquoted: escape any char + continuation",
		reflect.DeepEqual(split(`a\ b`), []string{"a b"}) &&
			reflect.DeepEqual(split(`a\\`), []string{`a\`}) &&
			reflect.DeepEqual(split("a\\\nb"), []string{"ab"}))
	check("concat & empty words (incl. \"\"'')",
		reflect.DeepEqual(split(`a"b"'c'`), []string{"abc"}) &&
			reflect.DeepEqual(split(`a '' b`), []string{"a", "", "b"}) &&
			reflect.DeepEqual(split(`""''`), []string{""}) &&
			reflect.DeepEqual(split(`a""`), []string{"a"}) &&
			len(split("\\\n")) == 0)
	check("3 distinguishable errors with offsets",
		errAt(`ab'cd`, words.ErrUnterminatedSingle, 2) &&
			errAt(`ab"cd`, words.ErrUnterminatedDouble, 2) &&
			errAt(`ab\`, words.ErrTrailingBackslash, 2) &&
			!errAt(`ab'cd`, words.ErrUnterminatedDouble, 2))
	check("identical across all split points", sameChunks([]string{
		`a"b"'c' 'd\$' e\ f "g\h" i"" ''j`,
		"a\\\nb \"c\\\nd\" 'e\nf' \\ \\\\\\",
		"\"a\\", "'a", "a\\", "\\\n",
	}))
	check("round trip Split(Quote(w)) == w", func() bool {
		lists := [][]string{{""}, {"a b", `'"\$`, "中\ne", ""}}
		for _, w := range lists {
			got, err := words.Split(words.Quote(w))
			if err != nil || !reflect.DeepEqual(got, w) {
				return false
			}
		}
		return true
	}())
	check("byte counter == input size", func() bool {
		in := strings.Repeat(`a"b\$c" 'd\e' f\ `, 1<<16)
		var lx lex.Lexer
		var ev []lex.Event
		for i := 0; i < len(in); i++ {
			ev = lx.Step(in[i], ev[:0])
		}
		return lx.Count() == len(in)
	}())
	fmt.Printf("TOTAL %d fail\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
