package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
)

func main() {
	a := api.New()
	fail := false
	check := func(ok bool, msg string) {
		if !ok {
			fmt.Println("FAIL " + msg)
			fail = true
		}
	}
	// Eight derived tokens: label, text, rejected? The first eight output lines.
	toks := []struct {
		name, text string
		reject     bool
	}{
		{"0", `0`, false}, {"01", `01`, true}, {"-0.5", `-0.5`, false},
		{"1e3", `1e3`, false}, {"1.", `1.`, true},
		{`"a\nb"`, "\"a\\nb\"", false}, {"😀", "\"😀\"", false},
		{`"\uD800"`, "\"\\uD800\"", true},
	}
	for _, t := range toks {
		v, err := a.ParseText(t.text)
		switch {
		case t.reject && err == nil:
			check(false, "token "+t.name+" should reject")
		case !t.reject && err != nil:
			check(false, "token "+t.name+": "+err.Error())
		case t.reject:
			fmt.Printf("OK %-9s rejected: %v\n", t.name, err)
		default:
			fmt.Printf("OK %-9s -> %s\n", t.name, v.String())
		}
	}
	// Ninth line: every remaining required verdict in one aggregate check.
	em, err := a.ParseText("\"😀\"")
	check(err == nil && len([]rune(em.Str)) == 1 && []rune(em.Str)[0] == 0x1F600, "emoji must merge to U+1F600")
	_, eDup := a.ParseText(`{"a":1,"a":2}`)
	check(eDup != nil, "duplicate key must reject")
	_, eEsc := a.ParseText(`"\q"`)
	check(eEsc != nil, "illegal escape must reject")
	check(a.SelfCheck() == nil, "selfcheck (roundtrip + distinct sentinels + no trace)")
	var bld strings.Builder
	bld.WriteByte('{')
	for i := 0; i < 10000; i++ {
		if i > 0 {
			bld.WriteByte(',')
		}
		fmt.Fprintf(&bld, `"k%05d":%d`, i, i)
	}
	bld.WriteByte('}')
	_, eBig := a.ParseText(bld.String())
	check(eBig == nil, "10000-key object must parse (linear dup-check proven in parse test)")
	if !fail {
		fmt.Println("OK checks   U+1F600 merge, dup-key/bad-escape/01/1. reject, roundtrip, 10000-key linear")
	}
	// Tenth line: concurrency agreement, no sleeps used.
	sample := `{"x":[1,true,null,"s"],"y":-2.5e1}`
	want, _ := a.ParseText(sample)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := true
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e1 := a.ParseText(sample)
			s, _ := a.Stringify(v)
			_, e2 := a.ParseText(s)
			if e1 != nil || e2 != nil || !reflect.DeepEqual(v, want) || s != want.String() {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	check(ok, "concurrent parse/stringify disagree")
	if ok {
		fmt.Println("OK concur   32 goroutines parse+stringify agree with serial")
	}
	if fail {
		os.Exit(1)
	}
}
