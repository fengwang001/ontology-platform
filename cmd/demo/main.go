// Command demo exercises the incremental Top-N materialised view.
// No arguments, no network; exit code 0 iff every judgment passes.
package main

import "errors"
import "fmt"
import "math/rand"
import "os"
import "sort"
import "strconv"
import "strings"
import "sync"

import "ontology/api"
import "ontology/rank"

var fails int

func report(n string, ok bool) {
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[ok] + n)
	if !ok {
		fails++
	}
}
func put(m map[string]int64, k string, v int64, in bool) {
	if in {
		m[k] = v
	} else {
		delete(m, k)
	}
}
func opOf(s string) api.Op {
	v, _ := strconv.ParseInt(s[2:], 10, 64)
	return api.Op{Key: s[1:2], Score: v, Add: s[0] == '+'}
}
func csig(em []api.Change) string {
	p := make([]string, len(em))
	for i, c := range em {
		m := "-"
		if c.Entering {
			m = "+"
		}
		p[i] = m + c.Key + strconv.FormatInt(c.Score, 10)
	}
	return strings.Join(p, " ")
}
func vsig(v []api.Row) string {
	p := make([]string, len(v))
	for i, r := range v {
		p[i] = r.Key + strconv.FormatInt(r.Score, 10)
	}
	return strings.Join(p, " ")
}
func oracle(live map[string]int64, n int) string {
	ks := make([]string, 0, len(live))
	for k := range live {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool {
		si, sj := live[ks[i]], live[ks[j]]
		return si > sj || si == sj && ks[i] < ks[j]
	})
	ks = ks[:min(n, len(ks))]
	for i, k := range ks {
		ks[i] = k + strconv.FormatInt(live[k], 10)
	}
	return strings.Join(ks, " ")
}
func main() {
	seq := strings.Fields("+a50 +b70 +c50 +d60 +e50 -b70 -e50 +f55 -d60 +g45")
	exp := strings.Split("+a50|+b70|+c50|-c50 +d60||-b70 +c50||-c50 +f55|-d60 +c50|", "|")
	mv, _ := api.New(3, 16)
	got, ok10 := make([]string, 10), true
	for i, s := range seq {
		em, err := mv.Apply([]api.Op{opOf(s)})
		if got[i] = csig(em); err != nil || got[i] != exp[i] {
			ok10 = false
		}
	}
	report("10 steps ["+strings.Join(got, " | ")+"]; #6 & #9 backfill c50", ok10)
	report("Top-N after step 10 = "+vsig(mv.View()), vsig(mv.View()) == "f55 a50 c50")
	rv, _ := api.New(4, 200)
	rng, live, hold, okR, okP := rand.New(rand.NewSource(20260924)), map[string]int64{}, map[string]int64{}, true, true
	for t := 0; t < 300; t++ {
		k := "k" + strconv.Itoa(rng.Intn(30))
		s, ex := live[k]
		if ex && rng.Intn(2) == 1 {
			continue
		}
		op := api.Op{Key: k, Score: map[bool]int64{true: s, false: int64(rng.Intn(7))}[ex], Add: !ex}
		em, err := rv.Apply([]api.Op{op})
		put(live, k, op.Score, op.Add)
		if err != nil || vsig(rv.View()) != oracle(live, 4) {
			okR = false
		}
		for _, c := range em {
			hs, has := hold[c.Key]
			okP = okP && c.Entering != has && (c.Entering || hs == c.Score)
			put(hold, c.Key, c.Score, c.Entering)
		}
		okP = okP && len(hold) == len(rv.View())
	}
	report("random add/retract equals batch recompute", okR)
	report("every changelog prefix is self-consistent", okP)
	nBad, es := 0, []error{}
	for _, x := range [][2]int{{0, 5}, {5, 3}} {
		if _, e := api.New(x[0], x[1]); errors.Is(e, api.ErrInvalidN) {
			nBad++
		}
	}
	fv, _ := api.New(2, 4)
	fv.Apply([]api.Op{{Key: "a", Score: 1, Add: true}, {Key: "b", Score: 1, Add: true}, {Key: "c", Score: 1, Add: true}, {Key: "d", Score: 1, Add: true}})
	try := func(op api.Op) error { _, e := fv.Apply([]api.Op{op}); return e }
	for _, c := range []struct {
		op   api.Op
		want error
	}{
		{api.Op{Key: "a", Score: 1, Add: true}, api.ErrDuplicateKey},
		{api.Op{Key: "q", Score: 1}, api.ErrMissingRow},
		{api.Op{Key: "z", Score: 1, Add: true}, api.ErrTooManyRows},
	} {
		if e := try(c.op); errors.Is(e, c.want) {
			nBad++
			es = append(es, e)
		}
	}
	report("four distinct decidable errors", nBad == 5 && len(es) == 3 && es[0] != es[1] && es[0] != es[2] && es[1] != es[2])
	l, before := fv.Live(), vsig(fv.View())
	_, eB := fv.Apply([]api.Op{{Key: "b", Score: 1}, {Key: "z", Score: 2, Add: true}, {Key: "q", Score: 2}})
	report("rejected batch leaves no trace", errors.Is(eB, api.ErrMissingRow) && fv.Live() == l && vsig(fv.View()) == before)
	report("comparison count logarithmic for m=100..10000", rank.CheckLogBudget([]int{100, 316, 1000, 3162, 10000}))
	base, sigs, wg := vsig(mv.View()), make([]string, 24), sync.WaitGroup{}
	wg.Add(24)
	for i := range sigs {
		go func(i int) { defer wg.Done(); sigs[i] = vsig(mv.View()) }(i)
	}
	wg.Wait()
	same := true
	for _, s := range sigs {
		if s != base {
			same = false
		}
	}
	report("concurrent readers get identical views", same)
	sc2, _ := api.New(3, 16)
	report("SelfCheck passes all four invariants", sc2.SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}
