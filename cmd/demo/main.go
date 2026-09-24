package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/khash"
	"ontology/samp"
)

var failed bool

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK  " + msg)
	} else {
		failed = true
		fmt.Println("FAIL " + msg)
	}
}

func main() {
	keys := []string{"gnj", "dzv", "gnk", "kcm", "dzv", "qjy", "a6m", "dzv"}
	wantBucket := []int{2499, 0, 2500, 6005, 0, 2000, 5000, 0}
	wantKept := []bool{true, true, false, false, true, true, false, true}
	eventsOK := true
	for i, k := range keys {
		if khash.Bucket(k) != wantBucket[i] || khash.Sampled(k, 2500) != wantKept[i] {
			eventsOK = false
		}
	}
	ok(eventsOK, "8 events: buckets match NOTES.md, kept 5 (gnj,dzv x3,qjy)")
	boundaryOK := !khash.Sampled("gnk", 2500) && khash.Sampled("gnk", 2501) &&
		!khash.Sampled("qjy", 2000) && khash.Sampled("qjy", 2500) && !khash.Sampled("a6m", 5000)
	ok(boundaryOK, "boundary buckets: gnk@2500, qjy@2000, a6m@5000 excluded (strict <)")
	s, err := samp.New(2500, 100)
	evs := make([]samp.Event, len(keys))
	for i, k := range keys {
		evs[i] = samp.Event{Key: k, V: int64(i)}
	}
	got, ferr := s.Feed(evs)
	added1, removed1, err1 := s.SetRate(2000)
	added2, removed2, err2 := s.SetRate(5000)
	ok(err == nil && ferr == nil && err1 == nil && err2 == nil && len(got) == 5 &&
		len(added1) == 0 && reflect.DeepEqual(removed1, []string{"qjy", "gnj"}),
		fmt.Sprintf("SetRate(2000): added=%v removed=%v", added1, removed1))
	ok(reflect.DeepEqual(added2, []string{"qjy", "gnj", "gnk"}) && len(removed2) == 0,
		fmt.Sprintf("SetRate(5000): added=%v removed=%v", added2, removed2))
	known := []string{"gnj", "dzv", "gnk", "kcm", "qjy", "a6m"}
	a1, _ := api.New(0, 100)
	_, ferr = a1.Feed(evs)
	rng, monoOK, prev := rand.New(rand.NewSource(323)), ferr == nil, 0
	for i := 0; i < 50; i++ {
		r := rng.Intn(10001)
		added, removed, err := a1.SetRate(r)
		aset, rset := map[string]bool{}, map[string]bool{}
		for _, k := range added {
			aset[k] = true
		}
		for _, k := range removed {
			rset[k] = true
		}
		for _, k := range known {
			if aset[k] != (khash.Sampled(k, r) && !khash.Sampled(k, prev)) ||
				rset[k] != (khash.Sampled(k, prev) && !khash.Sampled(k, r)) {
				monoOK = false
			}
		}
		if err != nil || r > prev && len(removed) > 0 || r < prev && len(added) > 0 {
			monoOK = false
		}
		prev = r
	}
	ok(monoOK, "random rate sequence: monotonic, added/removed match naive reference")
	a2, _ := api.New(2500, 100)
	a3, _ := api.New(2500, 100)
	_, _ = a2.Feed(evs)
	_, _ = a3.Feed(append([]samp.Event{}, append(evs[4:], evs[:4]...)...))
	agree := true
	for _, k := range known {
		if a2.Sampled(k) != a3.Sampled(k) {
			agree = false
		}
	}
	ok(agree, "two instances fed different orders agree on every key")
	_, eRate1 := api.New(10001, 10)
	_, eRate2 := api.New(0, 0)
	eBad, _ := api.New(2500, 1)
	_, _ = eBad.Feed([]samp.Event{{Key: "x"}})
	_, eEmpty := eBad.Feed([]samp.Event{{Key: ""}})
	_, eMany := eBad.Feed([]samp.Event{{Key: "y"}})
	errOK := errors.Is(eRate1, api.ErrRateRange) && errors.Is(eRate2, api.ErrRateRange) &&
		errors.Is(eEmpty, api.ErrEmptyKey) && errors.Is(eMany, api.ErrTooManyKeys) &&
		!errors.Is(eEmpty, api.ErrRateRange) && !errors.Is(eMany, api.ErrEmptyKey) &&
		!errors.Is(eRate1, api.ErrTooManyKeys)
	ok(errOK, "sentinel errors: ErrRateRange / ErrEmptyKey / ErrTooManyKeys distinct")
	a4, _ := api.New(2500, 3)
	batch := []samp.Event{{Key: "gnj"}, {Key: "dzv"}, {Key: "gnk"}}
	before, _ := a4.Feed(batch)
	_, _, _ = a4.SetRate(-1)
	_, _ = a4.Feed([]samp.Event{{Key: ""}})
	_, _ = a4.Feed([]samp.Event{{Key: "qjy"}})
	after, _ := a4.Feed(batch)
	sameAdded, sameRemoved, _ := a4.SetRate(2500)
	ok(reflect.DeepEqual(before, after) && len(sameAdded) == 0 && len(sameRemoved) == 0,
		"rejected ops leave rate and known keys unchanged")
	ok(samp.SelfCheck() == nil && api.SelfCheck() == nil,
		"probe count independent of m; api.SelfCheck passes")
	want := map[string]bool{}
	for _, k := range known {
		if khash.Sampled(k, 2500) {
			want[k] = true
		}
	}
	a5, _ := api.New(2500, 100)
	var wg sync.WaitGroup
	sets := make([]map[string]bool, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			perm := append([]samp.Event{}, evs...)
			rand.New(rand.NewSource(int64(g))).Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
			out, _ := a5.Feed(perm)
			set := map[string]bool{}
			for _, e := range out {
				set[e.Key] = true
			}
			sets[g] = set
		}(g)
	}
	wg.Wait()
	concOK := true
	for _, set := range sets {
		if !reflect.DeepEqual(set, want) {
			concOK = false
		}
	}
	ok(concOK, "8 goroutines x shuffled feeds: sampled key sets identical to naive")
	if failed {
		os.Exit(1)
	}
}
