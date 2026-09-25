// Command demo verifies the Aho-Corasick matcher end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"ontology/ac"
	"ontology/api"
	"ontology/trie"
)

var failed bool

func report(ok bool, name string) {
	failed = failed || !ok
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + name)
}
func checkSection3() bool {
	tr := trie.New()
	for i, p := range []string{"a", "ab", "bab"} {
		tr.Insert(p, i)
	}
	tr.Build()
	at := func(s string) *trie.Node {
		n := tr.Root()
		for i := 0; i < len(s); i++ {
			n, _ = n.Child(s[i])
		}
		return n
	}
	ok := at("a").Fail() == tr.Root() && at("b").Fail() == tr.Root() && at("ab").Fail() == at("b") && at("ba").Fail() == at("a") && at("bab").Fail() == at("ab") && tr.Root().Fail() == tr.Root()
	want := []ac.Match{{Pattern: 0, End: 1}, {Pattern: 1, End: 2}, {Pattern: 0, End: 3}, {Pattern: 2, End: 4}, {Pattern: 1, End: 4}}
	return ok && reflect.DeepEqual(ac.NewAutomaton(tr).Match("abab"), want)
}
func checkNaivePosition() bool {
	cases := []struct {
		pats []string
		text string
	}{
		{[]string{"a", "ab", "bab"}, "abab"},
		{[]string{"aa", "aaa"}, "aaaaa"},
		{[]string{"he", "she", "hers"}, "ushers"},
		{[]string{"ab", "ba", "aba"}, "ababa"},
		{[]string{"abc", "bc", "c", "ababc"}, "ababcabc"},
	}
	for _, c := range cases {
		if api.New(c.pats) != nil {
			return false
		}
		got, _ := api.Match(c.text)
		want := map[ac.Match]int{}
		total := 0
		for pi, p := range c.pats {
			for s := 0; s+len(p) <= len(c.text); s++ {
				if c.text[s:s+len(p)] == p {
					want[ac.Match{Pattern: pi, End: s + len(p)}]++
					total++
				}
			}
		}
		if len(got) != total {
			return false
		}
		for _, m := range got {
			if p := c.pats[m.Pattern]; m.End < len(p) || m.End > len(c.text) || c.text[m.End-len(p):m.End] != p {
				return false
			}
			if want[m]--; want[m] < 0 {
				return false
			}
		}
	}
	return true
}
func checkRejections() bool {
	_, nbErr := api.Match("x") // before any New: not built
	if api.New([]string{"he", "she", "hers"}) != nil {
		return false
	}
	base, _ := api.Match("ushers")
	_, textErr := api.Match("ab\xff")
	want := map[error]error{
		nbErr: api.ErrNotBuilt, api.New(nil): api.ErrEmptyPatterns,
		api.New([]string{""}): api.ErrEmptyPattern, api.New([]string{"\xff"}): api.ErrInvalidUTF8,
		textErr: api.ErrInvalidUTF8,
	}
	for err, sent := range want {
		for _, s := range []error{api.ErrEmptyPatterns, api.ErrEmptyPattern, api.ErrInvalidUTF8, api.ErrNotBuilt} {
			if errors.Is(err, s) != (s == sent) {
				return false
			}
		}
	}
	var u *api.UTF8Error
	if !errors.As(textErr, &u) || u.Offset != 2 {
		return false
	}
	after, err := api.Match("ushers")
	return err == nil && reflect.DeepEqual(base, after)
}
func checkLinear() bool {
	const n = 10000
	tr := trie.New()
	tr.Insert(strings.Repeat("a", 50), 0)
	tr.Insert("b", 1)
	tr.Build()
	a := ac.NewAutomaton(tr)
	a.Match(strings.Repeat("a", n))
	cmps := reflect.ValueOf(a).Elem().FieldByName("cmps").Int()
	return cmps > 0 && cmps <= 2*n
}
func checkConcurrent() bool {
	if api.New([]string{"a", "ab", "bab", "ba"}) != nil {
		return false
	}
	want, _ := api.Match("abababab")
	var wg sync.WaitGroup
	bad := make(chan bool, 8*50)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if got, _ := api.Match("abababab"); !reflect.DeepEqual(got, want) {
					bad <- true
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	return len(bad) == 0
}
func main() {
	report(checkRejections(), "four rejections distinguishable, state unchanged")
	report(checkSection3(), "fail chain + 5 matches on abab")
	report(checkNaivePosition(), "naive reference + position self-consistent")
	report(checkLinear(), "comparisons <= 2n at n=10000")
	report(checkConcurrent(), "concurrent Match identical")
	report(api.SelfCheck() == nil, "api.SelfCheck")
	if failed {
		os.Exit(1)
	}
}
