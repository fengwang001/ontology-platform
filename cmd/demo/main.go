package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"

	"ontology/api"
	"ontology/rank"
	"ontology/trie"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Println(status, name)
}

func main() {
	tr := trie.New()
	tr.Insert("app", 3)
	tr.Insert("apple", 5)
	tr.Delete("app")
	left := 0
	tr.Walk("app", func(string, int) { left++ })
	report("trie: delete app keeps apple, refs consistent", left == 1 && tr.CheckRefs() && tr.Count() == 1)

	tr2 := trie.New()
	tr2.Insert("apricot", 5)
	tr2.Insert("apple", 5)
	tr2.Insert("app", 3)
	tr2.Insert("application", 2)
	report("rank: Complete(ap,3)==[apple apricot app]",
		reflect.DeepEqual(rank.TopK(tr2, "ap", 3), []string{"apple", "apricot", "app"}))

	a := api.New()
	a.Insert("apricot", 5)
	a.Insert("apple", 5)
	a.Insert("app", 3)
	a.Insert("application", 2)
	got1, _ := a.Complete("ap", 3)
	a.Delete("app")
	got2, _ := a.Complete("ap", 3)
	report("api: top3 then delete app keeps apple+application",
		reflect.DeepEqual(got1, []string{"apple", "apricot", "app"}) &&
			reflect.DeepEqual(got2, []string{"apple", "apricot", "application"}) && a.Count() == 3)
	report("api: SelfCheck four invariants", api.New().SelfCheck() == nil)
	report("api: four distinguishable errors", checkErrors())
	report("api: rejected ops keep state", checkStateless())
	report("complexity: candidate count independent of m", checkComplexity())
	report("concurrency: disjoint inserts merge correctly", checkConcurrent())
	if failed {
		os.Exit(1)
	}
}

func checkErrors() bool {
	a := api.New()
	a.Insert("apple", 5)
	e1 := a.Insert("", 1)
	e2 := a.Insert("\xff", 1)
	e3 := a.Delete("ghost")
	_, e4 := a.Complete("a", 0)
	var u *api.UTF8Error
	return errors.Is(e1, api.ErrEmpty) && errors.As(e2, &u) && u.Offset == 0 &&
		errors.Is(e3, api.ErrNotFound) && errors.Is(e4, api.ErrBadK) &&
		!errors.Is(e1, e3) && !errors.Is(e1, e4) && !errors.Is(e3, e4)
}

func checkStateless() bool {
	a := api.New()
	a.Insert("apple", 5)
	a.Insert("", 9)
	a.Insert("\xff", 9)
	a.Delete("ghost")
	a.Complete("a", -1)
	got, _ := a.Complete("a", 10)
	return a.Count() == 1 && reflect.DeepEqual(got, []string{"apple"})
}

func checkComplexity() bool {
	const d = 7
	for _, m := range []int{100, 1000, 10000} {
		a := api.New()
		for i := 0; i < m; i++ {
			a.Insert(fmt.Sprintf("x%05d", i), i+1)
		}
		for i := 0; i < d; i++ {
			a.Insert(fmt.Sprintf("pre%d", i), i+1)
		}
		got, err := a.Complete("pre", m+d)
		if err != nil || len(got) != d {
			return false
		}
	}
	return true
}

func checkConcurrent() bool {
	const n, per = 8, 200
	a := api.New()
	ref := map[string]int{}
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				a.Insert(fmt.Sprintf("g%02d-%04d", g, i), i+1)
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		for i := 0; i < per; i++ {
			ref[fmt.Sprintf("g%02d-%04d", g, i)] = i + 1
		}
	}
	all := make([]string, 0, len(ref))
	for s := range ref {
		all = append(all, s)
	}
	sort.Slice(all, func(i, j int) bool {
		if ref[all[i]] != ref[all[j]] {
			return ref[all[i]] > ref[all[j]]
		}
		return all[i] < all[j]
	})
	got, _ := a.Complete("g", n*per)
	return a.Count() == n*per && reflect.DeepEqual(got, all)
}
