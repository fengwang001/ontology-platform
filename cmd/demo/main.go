// Command demo prints OK/FAIL judgements for the incremental GROUP BY engine.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sync"

	"ontology/api"
	"ontology/gagg"
	"ontology/gdelta"
)

type ch = gdelta.Change

func cg(g string, s, n int64, a bool) ch { return ch{G: g, Sum: s, Count: n, Add: a} }

func main() {
	fail := 0
	ok := func(n string, c bool) {
		fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[c] + n)
		if !c {
			fail++
		}
	}
	o := gdelta.Row{G: "a", V: 5}
	d1, d2 := gdelta.Deltas(&o, &gdelta.Row{G: "a", V: 8}), gdelta.Deltas(&o, &gdelta.Row{G: "b", V: 3})
	ok("gdelta net deltas + order", len(d1) == 1 && d1[0].DSum == 3 && len(d2) == 2 && d2[0].G == "a")
	t := gagg.New(3)
	want := [][]ch{{cg("a", 5, 1, true)}, {cg("a", 5, 1, false), cg("a", 0, 2, true)}, {cg("b", 7, 1, true)}, {cg("a", 0, 2, false), cg("a", 3, 2, true)}, {cg("a", 3, 2, false), cg("a", 8, 1, true), cg("b", 7, 1, false), cg("b", 10, 2, true)}, {cg("a", 8, 1, false), cg("c", 8, 1, true)}, {}, {cg("b", 10, 2, false), cg("b", 7, 1, true)}}
	ops := []gdelta.Op{gdelta.Insert(1, "a", 5), gdelta.Insert(2, "a", -5), gdelta.Insert(3, "b", 7), gdelta.Update(1, "a", 8), gdelta.Update(2, "b", 3), gdelta.Update(1, "c", 8), gdelta.Update(3, "b", 7), gdelta.Delete(2)}
	good := true
	for i, op := range ops { // exact per-step changelog of the eight steps
		got, _ := t.Apply([]gdelta.Op{op})
		good = good && reflect.DeepEqual(got, want[i])
	}
	vw := t.View()
	ok("eight-step changelog + final view", good && len(vw) == 2 && vw["b"] == (gdelta.Agg{Sum: 7, Count: 1}))
	e := api.New(3)
	b := true
	for _, s := range [][2]any{{api.Insert(1, "a", 5), 1}, {api.Insert(2, "a", -5), 2}, {api.Update(1, "a", 5), 0}, {api.Update(1, "a", 7), 2}, {api.Delete(1), 2}} {
		cs, err := e.Apply([]api.Op{s[0].(api.Op)})
		b = b && err == nil && len(cs) == s[1].(int) && (s[1].(int) != 2 || (!cs[0].Add && cs[1].Add))
	}
	cs, _ := e.Apply([]api.Op{api.Delete(2)})
	ok("sum0-kept / count0-retract-only / same-key one pair", b && len(cs) == 1 && !cs[0].Add && len(e.View()) == 0)
	ok("random seq = recompute + every prefix self-consistent", randomOK())
	ok("concurrent view consistent, never count==0", concurrentOK())
	ok("api SelfCheck four invariants", api.New(0).SelfCheck() == nil)
	ok("four distinct errors + atomic reject + O(1) reads", errorOK())
	if fail > 0 {
		os.Exit(1)
	}
}

func randomOK() bool {
	e, rng := api.New(20), rand.New(rand.NewSource(1))
	rows, down := map[int64]gdelta.Row{}, map[string]gdelta.Agg{}
	for i := 0; i < 2000; i++ {
		id, g, v := int64(rng.Intn(40)), fmt.Sprintf("g%d", rng.Intn(12)), int64(rng.Intn(21)-10)
		op := api.Insert(id, g, v)
		if _, x := rows[id]; x && rng.Intn(2) == 1 {
			op = api.Delete(id)
		} else if x {
			op = api.Update(id, g, v)
		}
		c, err := e.Apply([]api.Op{op})
		bad := err != nil
		for _, x := range c { // replay each prefix in a downstream view
			w := gdelta.Agg{Sum: x.Sum, Count: x.Count}
			cur, have := down[x.G]
			if x.Add == have || have && cur != w {
				bad = true
			} else if x.Add {
				down[x.G] = w
			} else {
				delete(down, x.G)
			}
		}
		if bad {
			continue
		}
		rows[id] = gdelta.Row{G: g, V: v}
		if op.Kind == gdelta.DeleteOp {
			delete(rows, id)
		}
	}
	want := map[string]gdelta.Agg{}
	for _, r := range rows {
		a := want[r.G]
		want[r.G] = gdelta.Agg{Sum: a.Sum + r.V, Count: a.Count + 1}
	}
	return reflect.DeepEqual(want, e.View()) && reflect.DeepEqual(down, e.View())
}

func concurrentOK() bool {
	e := api.New(40)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	go func() { // spinning reader, no sleep
		for {
			select {
			case <-stop:
				return
			default:
				for _, a := range e.View() {
					if a.Count <= 0 {
						panic("count<=0")
					}
				}
				runtime.Gosched()
			}
		}
	}()
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			g, x := fmt.Sprintf("g%d", w), fmt.Sprintf("x%d", w)
			for id := 4 * w; id < 4*w+4; id++ {
				e.Apply([]api.Op{api.Insert(int64(id), g, 1)})
			}
			e.Apply([]api.Op{api.Update(int64(4*w), x, 10)})
			e.Apply([]api.Op{api.Delete(int64(4*w + 1))})
			e.Apply([]api.Op{api.Update(int64(4*w+2), g, 3)})
		}(w)
	}
	wg.Wait()
	close(stop)
	want := map[string]gdelta.Agg{}
	for w := 0; w < 12; w++ {
		want[fmt.Sprintf("g%d", w)] = gdelta.Agg{Sum: 4, Count: 2}
		want[fmt.Sprintf("x%d", w)] = gdelta.Agg{Sum: 10, Count: 1}
	}
	return reflect.DeepEqual(want, e.View())
}

func errorOK() bool {
	t := gagg.New(1)
	_, e0 := t.Apply([]gdelta.Op{gdelta.Insert(1, "a", 1)})
	_, e1 := t.Apply([]gdelta.Op{gdelta.Insert(2, "b", 1)})
	_, e2 := t.Apply([]gdelta.Op{gdelta.Insert(5, "a", 2), gdelta.Insert(5, "a", 2)})
	_, e3 := t.Apply([]gdelta.Op{gdelta.Update(9, "a", 1)})
	_, e4 := t.Apply([]gdelta.Op{gdelta.Insert(2, "", 1)})
	return e0 == nil && errors.Is(e1, gagg.ErrTooMany) && errors.Is(e2, gagg.ErrRowExists) && errors.Is(e3, gagg.ErrRowNotFound) && errors.Is(e4, gagg.ErrEmptyGroup) && len(t.View()) == 1 && gagg.CheckReadsBounded() == nil
}
