// Command demo verifies the shell-style word splitter end to end.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/lex"
	"ontology/words"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
		return
	}
	fmt.Println("FAIL " + name)
	fails++
}

func splitOK(in string, want ...string) bool {
	got, err := words.Split(in)
	if err != nil || len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func errOK(in string, kind lex.Kind, off int) bool {
	_, err := words.Split(in)
	le, ok := err.(*lex.Error)
	return ok && le.Kind == kind && le.Offset == off
}

func splitPointsOK(in string) bool {
	want, werr := words.Split(in)
	for i := 0; i <= len(in); i++ {
		sp := words.NewSplitter()
		sp.Feed(in[:i])
		sp.Feed(in[i:])
		gerr := sp.Close()
		if (werr == nil) != (gerr == nil) {
			return false
		}
		if werr == nil && !splitOKStrings(sp.Words(), want) {
			return false
		}
	}
	return true
}

func splitOKStrings(a, b []string) bool {
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

func roundTripOK() bool {
	lists := [][]string{{""}, {"a b", "'", `"`, `\`, "$", "`", "\n", "é字"}}
	r := rand.New(rand.NewSource(7))
	alpha := []rune("xy ' \" \\ $` \n\té")
	for i := 0; i < 200; i++ {
		w := make([]string, r.Intn(4))
		for j := range w {
			rs := make([]rune, r.Intn(8))
			for k := range rs {
				rs[k] = alpha[r.Intn(len(alpha))]
			}
			w[j] = string(rs)
		}
		lists = append(lists, w)
	}
	for _, w := range lists {
		got, err := words.Split(words.Quote(w))
		if err != nil || !splitOKStrings(got, w) {
			return false
		}
	}
	return true
}

func counterOK() bool {
	chunk := `a"b\\c\"d" 'e\f' g\h` + "\\\n" + "i "
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString(chunk)
	}
	in := []byte(sb.String())
	l := lex.New(func(string) {})
	for i := 0; i < len(in); i++ {
		l.Feed(in[i : i+1])
	}
	return l.End() == nil && l.Processed() == len(in)
}

func main() {
	check("single-quote backslash literal", splitOK(`'a\b'`, `a\b`) && splitOK(`'a\'`, `a\`))
	check("double-quote escapes", splitOK(`"a\"b"`, `a"b`) && splitOK(`"a\\b"`, `a\b`) &&
		splitOK(`"a\b"`, `a\b`) && splitOK(`"a\$"`, `a$`) && splitOK("\"a\\\nb\"", "ab"))
	check("unquoted escape & continuation", splitOK(`a\ b`, "a b") &&
		splitOK(`a\\`, `a\`) && splitOK("a\\\nb", "ab"))
	check("concat & empty words", splitOK(`a"b"'c'`, "abc") &&
		splitOK(`a '' b`, "a", "", "b") && splitOK(`""''`, "") && splitOK(`a""`, "a") &&
		splitOK("\\\n") && splitOK("  \t "))
	check("three distinguishable errors", errOK(`ab 'cd`, lex.KindUnclosedSingle, 3) &&
		errOK(`x "yz`, lex.KindUnclosedDouble, 2) && errOK(`ab\`, lex.KindTrailingBackslash, 2))
	check("all split points consistent", splitPointsOK(`a\ b"c"$'d'`) &&
		splitPointsOK("\"a\\\nb\" 'x\\'") && splitPointsOK(`""''`) && splitPointsOK(`ab\`))
	check("quote round trip", roundTripOK())
	check("byte counter == input size", counterOK())
	fmt.Printf("TOTAL %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
