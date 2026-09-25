package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"

	"ontology/api"
	"ontology/store"
)

var fails int

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK:", name)
		return
	}
	fails++
	fmt.Println("FAIL:", name)
}

func main() {
	// Section 3: the prescribed T=4 sequence; verify delta/base after EACH step.
	st, _ := store.New(4)
	ops := []struct {
		del       bool
		k, v      string
		wantDelta int
	}{
		{false, "a", "1", 1}, {false, "b", "2", 2}, {false, "c", "3", 3},
		{true, "c", "", 0}, {false, "b", "9", 1}, {false, "c", "7", 2},
		{false, "c", "8", 3},
	}
	good := true
	for i, op := range ops {
		if op.del {
			_ = st.Del(op.k)
		} else {
			_ = st.Set(op.k, op.v)
		}
		bk := st.BaseKeys()
		good = good && st.DeltaLen() == op.wantDelta
		good = good && (i < 3 && len(bk) == 0 || i >= 3 && len(bk) == 2 && bk[0] == "a" && bk[1] == "b")
	}
	ok("seven steps: per-step delta/base, compaction at step 4", good)

	va, oka := st.Read("a")
	vb, okb := st.Read("b")
	vc, okc := st.Read("c")
	ok("reads a=1,b=9,c=8; Read(c) merge cost 3",
		oka && okb && okc && va == "1" && vb == "9" && vc == "8" && st.DeltaLen() == 3)

	s2, _ := store.New(4)
	_ = s2.Set("x", "1")
	_ = s2.Del("x")
	_, gone := s2.Read("x")
	_ = s2.Set("x", "2")
	vr, back := s2.Read("x")
	ok("deleted reads absent, rewrite returns latest, delta<T",
		!gone && back && vr == "2" && st.DeltaLen() < 4 && s2.DeltaLen() < 4)

	before := st.DeltaLen()
	e1, e2 := st.Set("", "z"), st.Del("")
	_, e3 := store.New(0)
	ok("two distinct decidable errors, state unchanged",
		errors.Is(e1, store.ErrEmptyKey) && errors.Is(e2, store.ErrEmptyKey) &&
			errors.Is(e3, store.ErrBadThreshold) && e1 != e3 && st.DeltaLen() == before)

	pub, _ := api.New(4)
	ok("api SelfCheck verifies all four invariants", pub.SelfCheck() == nil)

	bounded := true
	for _, m := range []int{100, 1000, 10000} {
		b, _ := api.New(8)
		for i := 0; i < m; i++ {
			_ = b.Set("k"+strconv.Itoa(i), "v")
		}
		_, _ = b.Read("k0") // merge cost == len(delta); counter stays unexported
		if b.DeltaLen() >= 8 {
			bounded = false
		}
	}
	ok("merge cost stays < T for m in {100,1000,10000}", bounded)

	const N = 16
	full, _ := api.New(64)
	for i := 0; i < N; i++ {
		_ = full.Set("g"+strconv.Itoa(i), "v"+strconv.Itoa(i))
	}
	var wg sync.WaitGroup
	snap := make([][]string, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			row := make([]string, N)
			for i := 0; i < N; i++ {
				v, okk := full.Read("g" + strconv.Itoa(i))
				if !okk {
					row[i] = "-"
				} else {
					row[i] = v
				}
			}
			snap[g] = row
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < N; g++ {
		for i := range snap[0] {
			if snap[g][i] != snap[0][i] {
				same = false
			}
		}
	}
	wc, _ := api.New(64)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			_ = wc.Set("w"+strconv.Itoa(g), strconv.Itoa(g))
		}(g)
	}
	wg.Wait()
	for g := 0; g < N; g++ {
		if v, okk := wc.Read("w" + strconv.Itoa(g)); !okk || v != strconv.Itoa(g) {
			same = false
		}
	}
	ok("concurrent reads identical, concurrent writes serial-equivalent", same)

	if fails > 0 {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
}
