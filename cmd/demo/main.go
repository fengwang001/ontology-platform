package main

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"ontology/dep"
	"ontology/sched"
	"os"
	"sync"
)

func eight() []api.Txn {
	w := [][]string{{"a"}, {"b"}, {"a", "c"}, {"d"}, {"b", "d"}, {"c"}, {"e"}, {"b", "e"}}
	r := [][]string{nil, {"a"}, nil, {"c"}, nil, {"b"}, {"a"}, nil}
	t := make([]api.Txn, 8)
	for i := range t {
		t[i] = api.Txn{Seq: int64(i + 1), Writes: w[i], Reads: r[i]}
	}
	return t
}
func share(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
func buildFull(seed int64, n, keys int) (*api.Engine, [][]string) {
	e, _ := api.New(3, n+1)
	r := rand.New(rand.NewSource(seed))
	ws := make([][]string, n)
	for i := 1; i <= n; i++ {
		ws[i-1] = []string{fmt.Sprintf("k%d", r.Intn(keys))}
		_ = e.Append([]api.Txn{{Seq: int64(i), Writes: ws[i-1]}})
	}
	return e, ws
}
func main() {
	fails := 0
	ck := func(name string, cond bool, detail string) {
		if !cond {
			fails++
		}
		tag := "OK"
		if !cond {
			tag = "FAIL"
		}
		fmt.Println(tag, name, detail)
	}
	e, _ := api.New(2, 64)
	_ = e.Append(eight())
	depths := make([]int, 8)
	for i := range depths {
		depths[i], _ = e.Depth(int64(i + 1))
	}
	pl := e.Rounds()
	ck("eight depths & P=2 rounds", fmt.Sprint(depths) == "[1 1 2 1 2 3 1 3]" &&
		fmt.Sprint(pl.RoundOf) == "[1 1 2 2 3 3 4 5]", fmt.Sprintf("d=%v r=%v", depths, pl.RoundOf))
	ck("total rounds & max parallel", pl.Total == 5 && e.MaxParallel() == 4,
		fmt.Sprintf("total=%d maxpar=%d", pl.Total, e.MaxParallel()))
	kv := e.Replay()
	ck("final keys a3 b8 c6 d5 e8", kv["a"] == 3 && kv["b"] == 8 && kv["c"] == 6 &&
		kv["d"] == 5 && kv["e"] == 8, fmt.Sprint(kv))
	gd := dep.New()
	r := rand.New(rand.NewSource(99))
	for i := 1; i <= 80; i++ {
		_ = gd.Add(dep.Txn{Seq: int64(i), Writes: []string{fmt.Sprintf("k%d", r.Intn(6))}})
	}
	ck("random seqs match O(n^2) naive", gd.VerifyDepths() == nil, "")
	legal := true
	for tr := 0; tr < 30; tr++ {
		g2, rr := dep.New(), rand.New(rand.NewSource(int64(tr)))
		n := 1 + rr.Intn(40)
		ww := make([][]string, n)
		for i := 1; i <= n; i++ {
			ww[i-1] = []string{fmt.Sprintf("k%d", rr.Intn(5))}
			_ = g2.Add(dep.Txn{Seq: int64(i), Writes: ww[i-1]})
		}
		ro := sched.Build(g2, 1+rr.Intn(4)).RoundOf
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if share(ww[i], ww[j]) && ro[j] <= ro[i] {
					legal = false
				}
			}
		}
	}
	ck("intersecting txns strictly round-ordered", legal, "")
	_, badParam := api.New(0, 4)
	ep, _ := api.New(2, 4)
	gap := ep.Append([]api.Txn{{Seq: 1, Writes: []string{"a"}}, {Seq: 3, Writes: []string{"c"}}})
	invalid := ep.Append([]api.Txn{{Seq: 1, Writes: nil}})
	full, _ := api.New(1, 1)
	_ = full.Append([]api.Txn{{Seq: 1, Writes: []string{"a"}}})
	capErr := full.Append([]api.Txn{{Seq: 2, Writes: []string{"b"}}})
	distinct := api.ErrBadParams != api.ErrSeqGap && api.ErrSeqGap != api.ErrInvalidTxn &&
		api.ErrInvalidTxn != api.ErrCapacity
	ck("four distinct decidable errors", errors.Is(badParam, api.ErrBadParams) && errors.Is(gap, api.ErrSeqGap) &&
		errors.Is(invalid, api.ErrInvalidTxn) && errors.Is(capErr, api.ErrCapacity) && distinct, "")
	ea, _ := api.New(2, 2)
	_ = ea.Append([]api.Txn{{Seq: 1, Writes: []string{"a"}}})
	before, _ := ea.Depth(1)
	rejected := ea.Append([]api.Txn{{Seq: 3, Writes: []string{"z"}}})
	_, still := ea.Depth(2)
	resume := ea.Append([]api.Txn{{Seq: 2, Writes: []string{"b"}}})
	d2, ok2 := ea.Depth(2)
	after, _ := ea.Depth(1)
	ck("state unchanged after rejected batch", rejected != nil && !still && resume == nil &&
		ok2 && d2 == 1 && before == after, "")
	ck("large-m check-count bounded (disjoint & shared key)", dep.CheckCostBound() == nil, "")
	ce, cw := buildFull(7, 200, 8)
	serial := map[string]int64{}
	for i, w := range cw {
		for _, k := range w {
			serial[k] = int64(i + 1)
		}
	}
	wp, concOK := ce.Rounds(), true
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, p := ce.Replay(), ce.Rounds()
			for k, v := range serial {
				if got[k] != v {
					concOK = false
				}
			}
			for i := range p.RoundOf {
				if p.RoundOf[i] != wp.RoundOf[i] {
					concOK = false
				}
			}
		}()
	}
	wg.Wait()
	ck("concurrent replay/rounds identical to serial", concOK, "")
	if fails > 0 {
		os.Exit(1)
	}
}
