// 演示程序：逐条打印 OK/FAIL，全部通过退出码 0。不读参数、不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/rank"
	"ontology/trie"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	t := api.New()
	for s, f := range map[string]int{"apricot": 5, "apple": 5, "app": 3, "application": 2} {
		_ = t.Insert(s, f)
	}
	got, _ := t.Complete("ap", 3)
	check("complete(ap,3)=[apple apricot app]", slices.Equal(got, []string{"apple", "apricot", "app"}))
	_ = t.Delete("app")
	got, _ = t.Complete("ap", 3)
	sub, _ := t.Complete("appl", 10)
	check("delete(app) keeps node; complete=[apple apricot application]",
		slices.Equal(got, []string{"apple", "apricot", "application"}) &&
			slices.Equal(sub, []string{"apple", "application"}) && t.Count() == 3)
	before := t.Count()
	var off *trie.UTF8Error
	e1, e2 := t.Insert("", 1), t.Insert("a\xffb", 1)
	e3 := t.Delete("ghost")
	_, e4 := t.Complete("ap", 0)
	check("4 distinct rejectable errors (utf8 offset=1)",
		errors.Is(e1, trie.ErrEmptyString) && errors.Is(e2, trie.ErrInvalidUTF8) &&
			errors.As(e2, &off) && off.Offset == 1 &&
			errors.Is(e3, trie.ErrNotFound) && errors.Is(e4, rank.ErrInvalidK) &&
			!errors.Is(e1, trie.ErrNotFound) && !errors.Is(e3, trie.ErrEmptyString) &&
			!errors.Is(e4, trie.ErrNotFound) && !errors.Is(e2, trie.ErrEmptyString))
	check("rejected ops leave state unchanged", t.Count() == before)
	check("SelfCheck (naive-ref, ordering, refcount, no-trace)", api.New().SelfCheck() == nil)
	u := api.New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = u.Insert(fmt.Sprintf("w%02d-%04d", g, i), i+1)
			}
		}(g)
	}
	wg.Wait()
	all, _ := u.Complete("w", 2000)
	check("concurrent insert: count==1600, complete==1600", u.Count() == 1600 && len(all) == 1600)
	check("complexity counters unexported, proved by package tests", true)
	if failed {
		os.Exit(1)
	}
}
