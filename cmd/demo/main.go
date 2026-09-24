package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/api"
	"ontology/dagg"
	"ontology/mset"
)

func report(n string, ok bool) { fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok], n) }
func msetOK() bool {
	m := mset.New()
	m.Add("a")
	dup := m.Add("a")
	c1, ok1 := m.Remove("a")
	c2, ok2 := m.Remove("a")
	_, ok3 := m.Remove("a")
	return !dup && !c1 && ok1 && c2 && ok2 && !ok3 && m.Distinct() == 0 && m.Len() == 0
}
func nineOK() bool {
	a := dagg.New(10)
	B := [][]dagg.Change{{dagg.C("g", "a", 1)}, {dagg.C("g", "b", 1)}, {dagg.C("g", "a", 1)}, {dagg.C("g", "a", -1)}, {dagg.C("g", "b", -1)}, {dagg.C("g", "b", -1)}, {dagg.C("g", "c", 1), dagg.C("g", "c", -1)}, {dagg.C("g", "a", -1)}, {dagg.C("g", "a", 1)}}
	want := []string{"[{1 g 1}]", "[{-1 g 1} {1 g 2}]", "[]", "[]", "[{-1 g 2} {1 g 1}]", "REJECT", "[]", "[{-1 g 1}]", "[{1 g 1}]"}
	n := 0
	for i, b := range B {
		out, err := a.Feed(b)
		if want[i] == "REJECT" {
			if !errors.Is(err, dagg.ErrWithdrawAbsent) || a.View()["g"] != 1 {
				return false
			}
		} else if err != nil || fmt.Sprint(out) != want[i] {
			return false
		} else {
			n += len(out)
		}
	}
	a2 := dagg.New(10)
	for i := 0; i < 5; i++ {
		_, _ = a2.Feed(B[i])
	}
	before := fmt.Sprint(a2.View())
	_, err := a2.Feed([]dagg.Change{dagg.C("g", "c", -1), dagg.C("g", "c", 1)})
	return n == 7 && len(a.Log()) == 7 && errors.Is(err, dagg.ErrWithdrawAbsent) && fmt.Sprint(a2.View()) == before
}
func randomOK() bool {
	rng := rand.New(rand.NewSource(42))
	e := api.New(1 << 20)
	mult := map[[2]string]int{}
	for t := 0; t < 300; t++ {
		b := make([]api.Change, 1+rng.Intn(4))
		for i := range b {
			b[i] = dagg.C(fmt.Sprintf("g%d", rng.Intn(3)), fmt.Sprintf("v%d", rng.Intn(10)), 1-2*rng.Intn(2))
		}
		out, err := e.Feed(b)
		if err != nil {
			continue
		}
		if api.CheckBatch(out) != nil {
			return false
		}
		for _, c := range b {
			k := [2]string{c.Group, c.Val}
			if mult[k] += c.Sign; mult[k] == 0 {
				delete(mult, k)
			}
		}
		v := map[string]int{}
		for k, x := range mult {
			if x > 0 {
				v[k[0]]++
			}
		}
		if fmt.Sprint(e.View()) != fmt.Sprint(v) || api.CheckLog(e.Log()) != nil {
			return false
		}
	}
	return true
}
func errorsOK() bool {
	e := api.New(1)
	want := []error{api.ErrWithdrawAbsent, api.ErrInvalidChange, api.ErrLimitExceeded}
	bad := [][]api.Change{{dagg.C("g", "a", -1)}, {dagg.C("g", "a", 0)}, {dagg.C("g", "a", 1), dagg.C("g", "b", 1)}}
	for i := range want {
		if _, err := e.Feed(bad[i]); !errors.Is(err, want[i]) {
			return false
		}
	}
	out, err := e.Feed([]api.Change{dagg.C("g", "a", 1)})
	return err == nil && len(out) == 1 && e.View()["g"] == 1
}
func fed(m int) *api.Engine {
	e := api.New(1 << 20)
	b := make([]api.Change, m)
	for i := range b {
		b[i] = dagg.C("g", fmt.Sprintf("v%05d", i), 1)
	}
	_, _ = e.Feed(b)
	return e
}
func scaleOK() bool {
	const m = 10000
	e := fed(m)
	o1, _ := e.Feed([]api.Change{dagg.C("g", "new", 1)})
	o2, _ := e.Feed([]api.Change{dagg.C("g", "v00000", 1)})
	o3, err := e.Feed([]api.Change{dagg.C("g", "v00001", -1)})
	return err == nil && len(o1) == 2 && len(o2) == 0 && len(o3) == 2 && e.View()["g"] == m
}
func concOK() bool {
	e := fed(200)
	const N = 16
	var wg sync.WaitGroup
	snaps := make([]string, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) { defer wg.Done(); snaps[i] = fmt.Sprint(e.View()) }(i)
	}
	wg.Wait()
	for _, s := range snaps[1:] {
		if s != snaps[0] {
			return false
		}
	}
	e2 := api.New(1 << 20)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			g := fmt.Sprintf("grp%02d", i)
			_, _ = e2.Feed([]api.Change{dagg.C(g, "x", 1), dagg.C(g, "y", 1)})
		}(i)
	}
	wg.Wait()
	return fmt.Sprint(e2.View()) == "map[grp00:2 grp01:2 grp02:2 grp03:2 grp04:2 grp05:2 grp06:2 grp07:2 grp08:2 grp09:2 grp10:2 grp11:2 grp12:2 grp13:2 grp14:2 grp15:2]"
}
func main() {
	report("nine batches: per-batch changelog; b6 & reversed b7 rejected", nineOK())
	report("mset: distinct crossings, multiplicity, zero-delete", msetOK())
	report("api SelfCheck: all four invariants", api.New(10).SelfCheck() == nil)
	report("random batches: view==recompute, every log prefix valid", randomOK())
	report("three distinct errors: judgeable, no trace, usable after", errorsOK())
	report("big m: single-change batches stay incremental", scaleOK())
	report("concurrency: equal reader snapshots, disjoint writers correct", concOK())
}
