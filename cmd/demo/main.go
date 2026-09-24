// Command demo exercises the idempotent sink end to end.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[cond], name)
}

func rec(p int, o int64, k string, v int64) api.Rec {
	return api.Rec{Partition: p, Offset: o, Key: k, Val: v}
}

func main() {
	section3()
	ok("selfcheck: 4 invariants", api.New(8).SelfCheck() == nil)
	naive()
	errorsDistinct()
	restartLargeM()
	concurrent()
	if failed {
		os.Exit(1)
	}
}

// section3 replays the 10 records of NOTES.md as single-record batches.
func section3() {
	s := api.New(4)
	all := []api.Rec{
		rec(0, 0, "a", 1), rec(0, 1, "a", 2), rec(1, 0, "b", 5), rec(1, 1, "b", 3), rec(0, 1, "a", 2),
		rec(0, 3, "a", 4), rec(1, 1, "b", 3), rec(0, 3, "a", 4), rec(1, 2, "a", 6), rec(0, 4, "b", 8),
	}
	wantApplied := []bool{true, true, true, true, false, true, false, false, true, true}
	good, prev := true, s.Duplicates()
	for i, r := range all {
		if i == 6 {
			s.Restart()
		}
		err := s.Write([]api.Rec{r})
		d := s.Duplicates()
		if err != nil || (d == prev) != wantApplied[i] {
			good = false
		}
		prev = d
	}
	t := s.Table()
	ok("section3: 10 decisions, a=13 b=16 W=[4,2] dup=3", good &&
		t["a"] == 13 && t["b"] == 16 && s.Watermark(0) == 4 && s.Watermark(1) == 2 && s.Duplicates() == 3)
}

func naive() {
	rng := rand.New(rand.NewSource(3))
	s := api.New(8)
	logs := make([][]api.Rec, 3)
	for p := range logs {
		for i := int64(0); i < 20; i++ {
			logs[p] = append(logs[p], rec(p, i, string(rune('a'+p)), i+1))
		}
	}
	seen := map[[2]int64]bool{}
	want := map[string]int64{}
	applied := make([]int, 3)
	good := true
	for b := 0; b < 40 && good; b++ {
		var batch []api.Rec
		for p := range logs {
			lo := rng.Intn(applied[p] + 1)
			hi := lo + rng.Intn(len(logs[p])-lo+1)
			for _, r := range logs[p][lo:hi] {
				batch = append(batch, r)
				k := [2]int64{int64(p), r.Offset}
				if !seen[k] {
					seen[k] = true
					want[r.Key] += r.Val
				}
			}
			applied[p] = max(applied[p], hi)
		}
		good = s.Write(batch) == nil
		if rng.Intn(4) == 0 {
			s.Restart()
		}
	}
	got := s.Table()
	eq := len(got) == len(want)
	for k, v := range want {
		eq = eq && got[k] == v
	}
	ok("naive reference under resend+restart", good && eq)
}

func errorsDistinct() {
	s := api.New(1)
	setup := s.Write([]api.Rec{rec(0, 0, "a", 1)})
	e1 := s.Write([]api.Rec{rec(0, 1, "", 1)})
	e2 := s.Write([]api.Rec{rec(0, 2, "a", 1), rec(0, 2, "a", 1)})
	e3 := s.Write([]api.Rec{rec(1, 0, "a", 1)})
	distinct := errors.Is(e1, api.ErrInvalidRecord) && errors.Is(e2, api.ErrOutOfOrder) &&
		errors.Is(e3, api.ErrTooManyPartitions) && e1 != e2 && e2 != e3 && e1 != e3
	ok("3 distinct errors, rejected batch leaves no trace", setup == nil && distinct &&
		s.Table()["a"] == 1 && s.Watermark(0) == 0 && s.Duplicates() == 0 &&
		s.Write([]api.Rec{rec(0, 1, "a", 1)}) == nil)
}

// restartLargeM: state survives Restart at m=10000 applied records.
func restartLargeM() {
	s := api.New(4)
	var batch []api.Rec
	for i := 0; i < 10000; i++ {
		batch = append(batch, rec(i%4, int64(i/4), fmt.Sprintf("k%d", i%3), 1))
	}
	good := s.Write(batch) == nil
	s.Restart()
	t := s.Table()
	ok("restart at m=10000 keeps state", good &&
		t["k0"] == 3334 && s.Watermark(0) == 2499 && s.Duplicates() == 0)
}

func concurrent() {
	const n = 8
	batch := []api.Rec{rec(0, 0, "a", 1), rec(1, 0, "b", 2)}
	s := api.New(4)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = s.Write(batch)
		}()
	}
	close(start)
	wg.Wait()
	ok("concurrent duplicate writes", s.Table()["a"] == 1 && s.Table()["b"] == 2 &&
		s.Duplicates() == int64((n-1)*len(batch)))
}
