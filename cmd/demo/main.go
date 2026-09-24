// 演示程序：行级过滤视图增量维护。不读参数、不联网，退出码 0。
package main

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/fpred"
	"ontology/fview"
)

var failed bool

func report(n string, ok bool) {
	s := "OK "
	if !ok {
		s, failed = "FAIL ", true
	}
	fmt.Println(s + n)
}

func ins(id string, v int64) fpred.Change {
	return fpred.Change{Kind: fpred.Insert, After: fpred.Row{ID: id, Val: v}}
}
func upd(id string, b, a int64) fpred.Change {
	return fpred.Change{Kind: fpred.Update, Before: fpred.Row{ID: id, Val: b}, After: fpred.Row{ID: id, Val: a}}
}
func del(id string, v int64) fpred.Change {
	return fpred.Change{Kind: fpred.Delete, Before: fpred.Row{ID: id, Val: v}}
}

func eightSteps() bool {
	fv, _ := api.New(10, 20)
	o := func(id string, v int64, a bool) []fpred.Out { return []fpred.Out{{Row: fpred.Row{ID: id, Val: v}, Add: a}} }
	chs := []fpred.Change{ins("x", 5), ins("y", 12), upd("x", 5, 15), upd("y", 12, 12),
		upd("y", 12, 20), upd("y", 20, 3), upd("x", 15, 10), del("y", 3)}
	want := [][]fpred.Out{nil, o("y", 12, true), o("x", 15, true), nil, o("y", 12, false), nil,
		append(o("x", 15, false), fpred.Out{Row: fpred.Row{ID: "x", Val: 10}, Add: true}), nil}
	for i, ch := range chs {
		if got, e := fv.Apply([]fpred.Change{ch}); e != nil || !slices.Equal(got, want[i]) {
			return false
		}
	}
	return maps.Equal(fv.View(), map[string]int64{"x": 10})
}

func boundary() bool {
	fv, _ := api.New(10, 20)
	_, e := fv.Apply([]fpred.Change{ins("lo", 10), ins("hi", 20), ins("mid", 19)})
	return e == nil && maps.Equal(fv.View(), map[string]int64{"lo": 10, "mid": 19})
}

func prefixOK(log []fpred.Out) bool {
	d := map[string]int64{}
	for _, o := range log {
		got, ok := d[o.Row.ID]
		if o.Add == ok || (!o.Add && got != o.Row.Val) {
			return false
		}
		if o.Add {
			d[o.Row.ID] = o.Row.Val
		} else {
			delete(d, o.Row.ID)
		}
	}
	return true
}

func randomOK() bool {
	for seed := int64(0); seed < 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		v, _ := api.New(10, 20)
		src, ids, seq, n := map[string]int64{}, []string{}, []fpred.Out{}, 0
		for t := 0; t < 250; t++ {
			var ch fpred.Change
			if len(ids) == 0 || r.Intn(4) == 0 {
				id, val := fmt.Sprintf("s%d-%d", seed, n), int64(r.Intn(30))
				n++
				ch, src[id], ids = ins(id, val), val, append(ids, id)
			} else if i := r.Intn(len(ids)); r.Intn(2) == 0 {
				nv := int64(r.Intn(30))
				ch, src[ids[i]] = upd(ids[i], src[ids[i]], nv), nv
			} else {
				i := r.Intn(len(ids))
				ch = del(ids[i], src[ids[i]])
				delete(src, ids[i])
				ids[i], ids = ids[len(ids)-1], ids[:len(ids)-1]
			}
			o, e := v.Apply([]fpred.Change{ch})
			if e != nil {
				return false
			}
			seq = append(seq, o...)
		}
		exp := map[string]int64{}
		for id, val := range src {
			if val >= 10 && val < 20 {
				exp[id] = val
			}
		}
		if !maps.Equal(v.View(), exp) || !prefixOK(seq) {
			return false
		}
	}
	return true
}

func errAndTrace() (errs, trace bool) {
	if _, e := api.New(20, 10); e != fpred.ErrInvalidRange {
		return false, false
	}
	fv, _ := api.New(10, 20)
	fv.Apply([]fpred.Change{ins("z", 15)})
	bad := []fpred.Change{ins("z", 1), del("nope", 1), upd("z", 9, 16),
		{Kind: fpred.Update, Before: fpred.Row{ID: "z", Val: 15}, After: fpred.Row{ID: "q"}}}
	want := []error{fview.ErrDuplicateKey, fview.ErrKeyNotFound, fview.ErrBeforeMismatch, fpred.ErrInvalidChange}
	errs = true
	for i, b := range bad {
		if _, e := fv.Apply([]fpred.Change{b}); e != want[i] {
			errs = false
		}
	}
	v0, s0 := fv.View(), fv.Source()
	_, e := fv.Apply([]fpred.Change{del("z", 1), ins("w", 15)})
	trace = e == fview.ErrBeforeMismatch && maps.Equal(v0, fv.View()) && maps.Equal(s0, fv.Source())
	_, e2 := fv.Apply([]fpred.Change{ins("w", 15)})
	return errs, trace && e2 == nil
}

func concurrentOK() bool {
	fv, _ := api.New(10, 20)
	for i := range 500 {
		fv.Apply([]fpred.Change{ins(fmt.Sprintf("c%d", i), int64(i%25))})
	}
	base, res, wg := fv.View(), make([]map[string]int64, 8), sync.WaitGroup{}
	for g := range res {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i] = fv.View() }(g)
	}
	wg.Wait()
	for _, m := range res {
		if !maps.Equal(base, m) {
			return false
		}
	}
	return true
}

func main() {
	errs, trace := errAndTrace()
	report("eight-step changelog per step + final view {x:10}", eightSteps())
	report("predicate boundary: Val=lo in, Val=hi out", boundary())
	report("20 random seqs: view==batch recompute, every log prefix consistent", randomOK())
	report("four distinct decidable sentinel errors", errs)
	report("rejected batch leaves no trace; instance still usable", trace)
	report("view lookups per Update stay constant as m=100..10000", fview.CheckLookupCost() == nil)
	report("8 concurrent readers get field-identical views", concurrentOK())
	report("api.SelfCheck verifies all four invariants", func() bool { f, _ := api.New(10, 20); return f.SelfCheck() == nil }())
	if failed {
		os.Exit(1)
	}
}
