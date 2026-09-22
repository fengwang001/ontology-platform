package main

import (
	"strings"
	"sync"

	"ontology/canon"
)

func limitsOK() bool {
	n := canon.New(canon.ModeOrdered, canon.Limits{
		MaxURLLength: 32, MaxPathSegments: 2, MaxQueryParams: 1})
	good := "http://h/a"
	before, err := n.Normalize(good)
	if err != nil {
		return false
	}
	kinds := map[canon.LimitKind]bool{}
	for _, u := range []string{
		"http://h/" + strings.Repeat("z", 64),
		"http://h/a/b/c",
		"http://h/p?a=1&b=2",
	} {
		le, ok := normalizeErr(u, n)
		if !ok {
			return false
		}
		kinds[le.Kind] = true
	}
	after, err := n.Normalize(good)
	return err == nil && len(kinds) == 3 && before.Canonical == after.Canonical
}

func normalizeErr(u string, n *canon.Normalizer) (*canon.LimitError, bool) {
	_, err := n.Normalize(u)
	le, ok := err.(*canon.LimitError)
	return le, ok
}

func stableOK(n *canon.Normalizer) bool {
	u := "HTTP://Example.COM.:080/a/./b?b=2&a=%7e"
	r1, err1 := n.Normalize(u)
	r2, err2 := n.Normalize(u)
	if err1 != nil || err2 != nil {
		return false
	}
	if r1.Canonical != r2.Canonical || !r1.Rewritten {
		return false
	}
	if len(r1.PathSegments) != len(r2.PathSegments) || len(r1.Query) != len(r2.Query) {
		return false
	}
	for i := range r1.PathSegments {
		if r1.PathSegments[i] != r2.PathSegments[i] {
			return false
		}
	}
	for i := range r1.Query {
		if r1.Query[i] != r2.Query[i] {
			return false
		}
	}
	return r1.RewriteKinds == r2.RewriteKinds
}

func concurrentOK(n *canon.Normalizer) bool {
	urls := []string{
		"HTTP://A.com:80/x/./y?b=2&a=1",
		"https://B.com.:4443/%41%2fb",
		"http://[2001:db8::1]:08080/p?q=%7e",
		"http://c.com/../../z",
	}
	serial := make([]string, len(urls))
	for i, u := range urls {
		r, err := n.Normalize(u)
		if err != nil {
			return false
		}
		serial[i] = r.Canonical
	}
	parallel := make([]string, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			if r, err := n.Normalize(u); err == nil {
				parallel[i] = r.Canonical
			}
		}(i, u)
	}
	wg.Wait()
	for i := range urls {
		if serial[i] != parallel[i] {
			return false
		}
	}
	return true
}
