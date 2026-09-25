package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/api"
	"ontology/hist"
	"ontology/store"
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

// traceHist runs the section-3 six-Put trace (K=3) on one key and verifies
// the physically retained versions and the cleaned version at each step.
func traceHist() {
	h := hist.New(3)
	vals := []string{"a", "b", "c", "d", "e", "f"}
	wantRetained := [][]int64{{1}, {1, 2}, {1, 2, 3}, {2, 3, 4}, {3, 4, 5}, {4, 5, 6}}
	wantCleaned := []int64{0, 0, 0, 1, 2, 3} // 0 = no cleanup at this step
	ok := true
	for i, val := range vals {
		if v := h.Put(val); v != int64(i+1) || h.Len() != len(wantRetained[i]) {
			ok = false
		}
		for _, wv := range wantRetained[i] {
			if got, hit := h.GetAt(wv); !hit || got != vals[wv-1] {
				ok = false
			}
		}
		if c := wantCleaned[i]; c > 0 {
			if _, hit := h.GetAt(c); hit {
				ok = false
			}
		}
	}
	check("hist six-put trace (retained + eager cleanup)", ok)
}

// traceStore checks multi-key independence and that Get matches a naive
// replay (last Put wins) across keys.
func traceStore() {
	s := store.New(3)
	replay := map[string]string{}
	ok := true
	for i := 0; i < 20; i++ {
		key := string(rune('a' + i%4))
		s.Put(key, fmt.Sprintf("v%d", i))
		replay[key] = fmt.Sprintf("v%d", i)
	}
	for key, want := range replay {
		got, hit := s.Get(key)
		if !hit || got != want || s.Len(key) != 3 {
			ok = false
		}
	}
	if _, hit := s.Get("never-written"); hit {
		ok = false
	}
	check("store multi-key + Get equals naive replay", ok)
}

// traceAPI checks the step-3/4 visibility boundary, the three distinct
// decidable errors, and that rejections leave state untouched.
func traceAPI() {
	a, err := api.New(3)
	ok := err == nil
	for _, v := range []string{"a", "b", "c"} {
		if _, err := a.Put("k", v); err != nil {
			ok = false
		}
	}
	_, hit1, _ := a.GetAt("k", 1)
	check("step3: no early cleanup, GetAt(1) hits", ok && hit1 && a.Len("k") == 3)
	if _, err := a.Put("k", "d"); err != nil {
		ok = false
	}
	_, hit1, _ = a.GetAt("k", 1)
	v2, hit2, _ := a.GetAt("k", 2)
	check("step4: GetAt(1) cleaned, GetAt(2)==b hits", !hit1 && hit2 && v2 == "b")

	_, e1 := a.Put("", "x")
	_, _, e2 := a.GetAt("k", 0)
	_, e3 := api.New(0)
	check("three distinct sentinel errors",
		errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrInvalidVersion) &&
			errors.Is(e3, api.ErrInvalidK) && !errors.Is(e1, e2) && !errors.Is(e2, e3))

	v, err := a.Put("k", "e")
	check("after rejections: next version is 5, state intact",
		err == nil && v == 5 && a.Len("k") == 3)
	b, _ := api.New(2)
	check("api.SelfCheck", b.SelfCheck() == nil)
}

// traceScale checks bounded cleanup at large m (per-Put cleanup moves <= 1
// is proven by hist's internal test) and identical concurrent reads.
func traceScale() {
	a, _ := api.New(5)
	for i := 0; i < 10000; i++ {
		if _, err := a.Put("k", fmt.Sprintf("v%d", i)); err != nil {
			check("scale put", false)
		}
	}
	latest, _ := a.Get("k")
	check("large-m: Len stays K, Get is latest", a.Len("k") == 5 && latest == "v9999")

	const n = 32
	results := make(chan [2]string, n)
	for i := 0; i < n; i++ {
		go func() {
			g, _ := a.Get("k")
			results <- [2]string{g, fmt.Sprint(a.Len("k"))}
		}()
	}
	ok := true
	for i := 0; i < n; i++ {
		if r := <-results; r[0] != latest || r[1] != "5" {
			ok = false
		}
	}
	check("concurrent readers see identical Get/Len", ok)
}

func main() {
	traceHist()
	traceStore()
	traceAPI()
	traceScale()
	if failed {
		os.Exit(1)
	}
}
