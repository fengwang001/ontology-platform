package main

import "errors"
import "fmt"
import "math/rand"
import "os"
import "strings"
import "sync"
import "sync/atomic"
import "ontology/api"
import "ontology/exc"

func report(name string, ok bool) {
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}
func rejects(w *api.View, c api.Change, want error) bool {
	_, err := w.Apply([]api.Change{c})
	return errors.Is(err, want)
}
func recalc(l, r map[string]int) map[string]int {
	o := map[string]int{}
	for x, n := range l {
		if d := n - r[x]; d > 0 {
			o[x] = d
		}
	}
	return o
}

type ref struct {
	l, r map[string]int
	rng  *rand.Rand
}

func (m *ref) next() api.Change {
	s := api.Side(1 + m.rng.Intn(2)) // L=1, R=2
	mm := [2]map[string]int{m.l, m.r}[s-1]
	x := fmt.Sprintf("k%c", 'a'+m.rng.Intn(12))
	d := 1 + m.rng.Intn(3)
	if mm[x] > 0 && m.rng.Intn(2) == 0 {
		d = -(1 + m.rng.Intn(mm[x])) // delete 1..cnt: always legal
	}
	mm[x] += d
	return api.Change{Side: s, Row: x, Delta: d}
}

func tenStep() (string, bool) {
	mk := func(s api.Side, x string, d int) api.Change { return api.Change{Side: s, Row: x, Delta: d} }
	ten := []api.Change{
		mk(api.R, "a", 1), mk(api.L, "a", 1), mk(api.L, "a", 1), mk(api.L, "a", 2), mk(api.R, "a", 1),
		mk(api.R, "a", 3), mk(api.L, "a", -1), mk(api.R, "a", -4), mk(api.L, "b", 1), mk(api.R, "b", 1),
	}
	want := []string{"-", "-", "a:+1", "a:+2", "a:-1", "a:-2", "-", "a:+2", "b:+1", "b:-1"}
	v, toks, ok := api.New(100), make([]string, 10), true
	for i, c := range ten {
		outs, err := v.Apply([]api.Change{c})
		if err != nil {
			return "", false
		}
		tok := "-"
		if len(outs) == 1 {
			tok = fmt.Sprintf("%s:%+d", outs[0].Row, outs[0].Delta)
		}
		if len(outs) > 1 || tok != want[i] {
			ok = false
		}
		toks[i] = tok
	}
	return strings.Join(toks, " ") + " | " + fmt.Sprint(v.View()), ok && v.View()["a"] == 2
}

func main() {
	all := true
	rpt := func(name string, ok bool) { report(name, ok); all = all && ok }
	log, ok10 := tenStep()
	rpt("ten-step logs: "+log, ok10)

	m, v := &ref{map[string]int{}, map[string]int{}, rand.New(rand.NewSource(2026))}, api.New(64)
	pOK, nOK, mOK := true, true, true
	for i := 0; i < 200; i++ {
		c := m.next()
		outs, err := v.Apply([]api.Change{c})
		if err != nil || fmt.Sprint(v.View()) != fmt.Sprint(recalc(m.l, m.r)) {
			pOK = false
		}
		for _, n := range v.View() {
			if n <= 0 {
				nOK = false
			}
		}
		if len(outs) > 1 {
			mOK = false
		}
		if len(outs) == 1 {
			o := outs[0]
			if o.Row != c.Row || o.Delta == 0 || o.Delta*o.Delta > c.Delta*c.Delta {
				mOK = false
			}
		}
	}
	rpt("random prefixes match batch recompute", pOK)
	rpt("view multiplicities always non-negative", nOK)
	rpt("change log minimal (<=1 output, |d|<=|Delta|)", mOK)

	w := api.New(10)
	eOK := rejects(w, api.Change{Side: api.L, Row: "", Delta: 1}, api.ErrInvalidChange)
	eOK = rejects(w, api.Change{Side: api.L, Row: "a", Delta: 0}, api.ErrInvalidChange) && eOK
	eOK = rejects(w, api.Change{Side: api.Side(9), Row: "a", Delta: 1}, api.ErrInvalidChange) && eOK
	eOK = rejects(w, api.Change{Side: api.L, Row: "a", Delta: -1}, api.ErrUnderflow) && eOK
	rpt("three judgeable, distinct sentinel errors", eOK)

	before := fmt.Sprint(w.View())
	_, err := w.Apply([]api.Change{{Side: api.L, Row: "q", Delta: 1}, {Side: api.L, Row: "q", Delta: -9}})
	tOK := errors.Is(err, api.ErrUnderflow) && fmt.Sprint(w.View()) == before
	_, err = w.Apply([]api.Change{{Side: api.R, Row: "q", Delta: 1}})
	rpt("rejected batch leaves no trace; view reused", tOK && err == nil)
	rpt("touched entries bounded m=100,1000,10000 (<=6)", exc.CheckIncrementalCost())

	cv, cm := api.New(64), &ref{map[string]int{}, map[string]int{}, rand.New(rand.NewSource(7))}
	var bounds sync.Map
	bounds.Store(fmt.Sprint(map[string]int{}), true)
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20000; j++ {
				if _, hit := bounds.Load(fmt.Sprint(cv.View())); !hit {
					bad.Store(true)
					return
				}
			}
		}()
	}
	for i := 0; i < 40; i++ {
		c := cm.next()
		bounds.Store(fmt.Sprint(recalc(cm.l, cm.r)), true)
		if _, err := cv.Apply([]api.Change{c}); err != nil {
			bad.Store(true)
		}
	}
	wg.Wait()
	rpt("concurrent readers see only batch-boundary views", !bad.Load())
	rpt("SelfCheck", api.New(1).SelfCheck() == nil)
	if !all {
		os.Exit(1)
	}
}
