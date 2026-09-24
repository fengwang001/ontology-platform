// 演示程序：逐项打印 OK/FAIL，退出码 0 表示全部通过。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
)

func u(k string, v int64, s string) api.Event { return api.Event{Op: 'U', Key: k, Ver: v, Val: s} }
func d(k string, v int64) api.Event           { return api.Event{Op: 'D', Key: k, Ver: v} }

func main() {
	allOK := true
	report := func(name string, good bool) {
		allOK = allOK && good
		fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[good], name)
	}
	keys := []string{"k1", "k2", "k3", "k4"}
	tab, _ := api.New(10, 100)
	steps := []struct {
		e     api.Event
		rows  map[string]api.Row
		tombs map[string]int64
		g, ig int64
	}{
		{u("k1", 5, "a"), map[string]api.Row{"k1": {Val: "a", Ver: 5}}, map[string]int64{}, 5, 0},
		{d("k1", 8), map[string]api.Row{}, map[string]int64{"k1": 8}, 8, 0},
		{u("k1", 6, "b"), map[string]api.Row{}, map[string]int64{"k1": 8}, 8, 1},
		{u("k2", 12, "c"), map[string]api.Row{"k2": {Val: "c", Ver: 12}}, map[string]int64{"k1": 8}, 12, 1},
		{u("k1", 8, "d"), map[string]api.Row{"k2": {Val: "c", Ver: 12}}, map[string]int64{"k1": 8}, 12, 2},
		{d("k3", 15), map[string]api.Row{"k2": {Val: "c", Ver: 12}}, map[string]int64{"k1": 8, "k3": 15}, 15, 2},
		{u("k2", 18, "e"), map[string]api.Row{"k2": {Val: "e", Ver: 18}}, map[string]int64{"k3": 15}, 18, 2},
		{u("k1", 7, "f"), map[string]api.Row{"k1": {Val: "f", Ver: 7}, "k2": {Val: "e", Ver: 18}}, map[string]int64{"k3": 15}, 18, 2},
		{u("k3", 14, "g"), map[string]api.Row{"k1": {Val: "f", Ver: 7}, "k2": {Val: "e", Ver: 18}}, map[string]int64{"k3": 15}, 18, 3},
		{u("k4", 30, "h"), map[string]api.Row{"k1": {Val: "f", Ver: 7}, "k2": {Val: "e", Ver: 18}, "k4": {Val: "h", Ver: 30}}, map[string]int64{}, 30, 3},
	}
	bad := map[int]bool{}
	for i, s := range steps {
		if err := tab.Apply([]api.Event{s.e}); err != nil {
			bad[i] = true
			continue
		}
		tm := map[string]int64{}
		for _, k := range keys {
			if v, ok := tab.Tomb(k); ok {
				tm[k] = v
			}
		}
		ig, g := tab.Stats()
		if !reflect.DeepEqual(tab.GetMany(keys), s.rows) || !reflect.DeepEqual(tm, s.tombs) || g != s.g || ig != s.ig {
			bad[i] = true
		}
	}
	report("ten-step trace: rows/tombs/G/ignored per step", len(bad) == 0)
	report("step5 equal Ver ignored; step3 old write blocked by tomb", !bad[2] && !bad[4])
	report("step7 tomb purged (G-t>=R); step8 old U applied", !bad[6] && !bad[7])
	sc, _ := api.New(1, 1)
	report("selfcheck: naive-ref consistency & 4 invariants", sc.SelfCheck() == nil)
	_, e1 := api.New(0, 1)
	et, _ := api.New(1, 1)
	e2 := et.Apply([]api.Event{{Op: 'X', Key: "k", Ver: 1}})
	_ = et.Apply([]api.Event{u("a", 1, "x")})
	e3 := et.Apply([]api.Event{u("b", 2, "y")})
	report("three distinguishable sentinel errors", errors.Is(e1, api.ErrBadParam) && errors.Is(e2, api.ErrBadEvent) &&
		errors.Is(e3, api.ErrTooMany) && !errors.Is(e1, api.ErrBadEvent) && !errors.Is(e2, api.ErrTooMany) && !errors.Is(e3, api.ErrBadParam))
	nt, _ := api.New(1<<60, 2)
	_ = nt.Apply([]api.Event{u("x", 1, "a")})
	ig0, g0 := nt.Stats()
	_ = nt.Apply([]api.Event{u("y", 2, "b"), u("z", 3, "c")})
	ig1, g1 := nt.Stats()
	r, has := nt.Get("x")
	report("rejected batch leaves no trace", ig1 == ig0 && g1 == g0 && has && r == (api.Row{Val: "a", Ver: 1}))
	lm, _ := api.New(1<<60, 20000)
	for i := 1; i <= 10000; i++ {
		_ = lm.Apply([]api.Event{d(fmt.Sprintf("k%d", i), int64(i))})
	}
	_ = lm.Apply([]api.Event{u("probe", 10001, "p")})
	_, gl := lm.Stats()
	_, tombKept := lm.Tomb("k1")
	report("large-m purge correct (checked-counter bound in store tests)", gl == 10001 && tombKept)
	ct, _ := api.New(1<<60, 100)
	var stop, half atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				s := ct.GetMany(keys)
				if len(s) == 0 {
					continue
				}
				for _, k := range keys {
					if r, ok := s[k]; !ok || r.Ver != s[keys[0]].Ver {
						half.Store(true)
						return
					}
				}
			}
		}()
	}
	for b := int64(1); b <= 50; b++ {
		_ = ct.Apply([]api.Event{u("k1", b, "x"), u("k2", b, "x"), u("k3", b, "x"), u("k4", b, "x")})
	}
	stop.Store(true)
	wg.Wait()
	report("concurrent GetMany never sees half batch", !half.Load())
	if !allOK {
		os.Exit(1)
	}
}
