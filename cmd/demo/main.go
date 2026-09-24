package main

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
)

func report(name string, ok bool) bool {
	s := "OK"
	if !ok {
		s = "FAIL"
	}
	fmt.Printf("%s: %s\n", name, s)
	return ok
}
func snap(s *api.Set, r int) map[string]api.Record { m, _ := s.Records(r); return m }
func op(s *api.Set, n map[string]api.Record, r int, e string, ts int64, add bool) {
	rec := n[e]
	if add {
		s.Add(r, e, ts)
		rec.A = max(rec.A, ts)
	} else {
		s.Remove(r, e, ts)
		rec.R = max(rec.R, ts)
	}
	n[e] = rec
}
func allMatch(s *api.Set, n int, parts ...map[string]api.Record) bool {
	naive := map[string]api.Record{}
	for _, nm := range parts {
		for e, rec := range nm {
			naive[e] = api.Record{A: max(naive[e].A, rec.A), R: max(naive[e].R, rec.R)}
		}
	}
	for r := 0; r < n; r++ {
		if !maps.Equal(snap(s, r), naive) {
			return false
		}
	}
	return true
}
func elevenSteps() ([][2]string, map[string]api.Record, map[string]api.Record) {
	s, _ := api.New(2, 100)
	ops := []func(){
		func() { s.Add(0, "x", 5) }, func() { s.Remove(1, "x", 5) }, func() { s.Add(1, "y", 3) }, func() { s.Remove(1, "z", 9) }, func() { s.Merge(0, 1) }, func() { s.Add(0, "z", 8) }, func() { s.Add(0, "x", 6) }, func() { s.Remove(0, "y", 7) }, func() { s.Add(1, "y", 6) }, func() { s.Merge(0, 1) }, func() { s.Merge(1, 0) },
	}
	var sets [][2]string
	var recs5 map[string]api.Record
	for i, o := range ops {
		o()
		if i == 4 {
			recs5 = snap(s, 0)
		}
		a, _ := s.Elements(0)
		b, _ := s.Elements(1)
		sets = append(sets, [2]string{fmt.Sprint(a), fmt.Sprint(b)})
	}
	return sets, recs5, snap(s, 0)
}
func main() {
	sets, recs5, recs := elevenSteps()
	line, want := "11-step", "11-step 1:[x]|[] 2:[x]|[] 3:[x]|[y] 4:[x]|[y] 5:[y]|[y] 6:[y]|[y] 7:[x y]|[y] 8:[x]|[y] 9:[x]|[y] 10:[x]|[y] 11:[x]|[x]"
	for i, ab := range sets {
		line += fmt.Sprintf(" %d:%s|%s", i+1, ab[0], ab[1])
	}
	all := report(line, line == want)
	all = report("step5 tie A==R removes", recs5["x"] == (api.Record{A: 5, R: 5}) && sets[4][0] == "[y]") && all
	all = report(fmt.Sprintf("step10 A records %v", recs), maps.Equal(recs, map[string]api.Record{"x": {A: 6, R: 5}, "y": {A: 6, R: 7}, "z": {A: 8, R: 9}})) && all
	st, _ := api.New(4, 10000)
	rng, naive := rand.New(rand.NewSource(42)), map[string]api.Record{}
	for i := 0; i < 500; i++ {
		r, e, ts, add := rng.Intn(4), string(rune('a'+rng.Intn(10))), int64(rng.Intn(15)+1), rng.Intn(2) == 0
		op(st, naive, r, e, ts, add)
	}
	st.SyncAll()
	all = report("random-vs-naive", allMatch(st, 4, naive)) && all
	mk := func() *api.Set {
		t, _ := api.New(3, 100)
		t.Add(0, "a", 1)
		t.Add(1, "b", 2)
		t.Remove(2, "a", 3)
		return t
	}
	x, y := mk(), mk()
	x.Merge(0, 1)
	y.Merge(1, 0)
	laws := maps.Equal(snap(x, 0), snap(y, 1)) // commutativity
	x, y = mk(), mk()
	x.Merge(0, 1)
	x.Merge(0, 2)
	y.Merge(1, 2)
	y.Merge(0, 1)
	laws = laws && maps.Equal(snap(x, 0), snap(y, 0)) // associativity
	b4 := snap(x, 0)
	x.Merge(0, 0)
	x.Merge(0, 2)
	all = report("merge laws", laws && maps.Equal(b4, snap(x, 0))) && all // idempotency
	big, _ := api.New(2, 20000)
	for i := 0; i < 10000; i++ {
		big.Add(1, fmt.Sprintf("e%d", i), 1)
	}
	big.Merge(0, 1)
	big.Add(1, "zz", 2)
	big.Remove(1, "e5", 3)
	big.Merge(0, 1)
	el, _ := big.Elements(0)
	m0 := snap(big, 0)
	all = report("incremental==full at m=10000 (count in tests)", len(el) == 10000 && m0["zz"] == (api.Record{A: 2}) && m0["e5"] == (api.Record{A: 1, R: 3})) && all
	e2, _ := api.New(2, 1)
	e2.Add(0, "a", 1)
	before := snap(e2, 0)
	sents := []error{api.ErrParam, api.ErrElement, api.ErrTimestamp, api.ErrCapacity}
	errs := []error{e2.Add(2, "x", 1), e2.Add(0, "", 1), e2.Add(0, "b", 0), e2.Add(0, "b", 2)}
	ok4 := true
	for i := range errs {
		ok4 = ok4 && errors.Is(errs[i], sents[i]) && !errors.Is(errs[i], sents[(i+1)%4])
	}
	all = report("four distinct errors", ok4) && all
	all = report("rejected-no-trace", maps.Equal(before, snap(e2, 0))) && all
	cc, _ := api.New(4, 100000)
	naives := []map[string]api.Record{{}, {}, {}, {}}
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				if g >= 4 {
					cc.Merge(r.Intn(4), r.Intn(4))
					continue
				}
				e, ts, add := string(rune('a'+r.Intn(10))), int64(r.Intn(30)+1), r.Intn(2) == 0
				op(cc, naives[g], g, e, ts, add)
			}
		}(g)
	}
	wg.Wait()
	cc.SyncAll()
	all = report("concurrent+SyncAll==naive", allMatch(cc, 4, naives...)) && all
	if !all {
		os.Exit(1)
	}
}
