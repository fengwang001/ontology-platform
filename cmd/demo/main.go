// Command demo verifies EBR end to end and prints one OK/FAIL per property.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var fails int

func report(name string, good bool) {
	tag := "OK  "
	if !good {
		tag, fails = "FAIL ", fails+1
	}
	fmt.Println(tag + name)
}
func retMap(s api.State) map[int]int64 {
	m := map[int]int64{}
	for _, it := range s.Retired {
		m[it.Node] = it.Epoch
	}
	return m
}
func eightSteps() bool {
	e := api.New()
	type row struct {
		run      func() []int
		g        int64
		act, ret map[int]int64
		rec      []int
	}
	tbl := []row{
		{func() []int { _ = e.Enter(1); return nil }, 0, map[int]int64{1: 0}, map[int]int64{}, nil},
		{func() []int { _ = e.Enter(2); return nil }, 0, map[int]int64{1: 0, 2: 0}, map[int]int64{}, nil},
		{func() []int { _ = e.Retire(10); return nil }, 0, map[int]int64{1: 0, 2: 0}, map[int]int64{10: 0}, nil},
		{func() []int { _ = e.Exit(1); return nil }, 0, map[int]int64{2: 0}, map[int]int64{10: 0}, nil},
		{func() []int { e.AdvanceEpoch(); return nil }, 1, map[int]int64{2: 0}, map[int]int64{10: 0}, nil},
		{e.Reclaim, 1, map[int]int64{2: 0}, map[int]int64{10: 0}, []int{}},
		{func() []int { _ = e.Exit(2); return nil }, 1, map[int]int64{}, map[int]int64{10: 0}, nil},
		{e.Reclaim, 1, map[int]int64{}, map[int]int64{}, []int{10}},
	}
	for _, r := range tbl {
		rec, s := r.run(), e.Snapshot() // r.run() evaluates before Snapshot()
		if s.G != r.g || !reflect.DeepEqual(s.Active, r.act) ||
			!reflect.DeepEqual(retMap(s), r.ret) || !reflect.DeepEqual(rec, r.rec) {
			return false
		}
	}
	return true
}
func main() {
	report("eight steps: G/active/retired/Reclaim match table", eightSteps())
	u := api.New()
	_ = u.Enter(2)
	_ = u.Retire(10)
	s := u.Snapshot() // T2 announced at ep0 == A's retirement epoch
	report("freeing A right at Retire would UAF; correct impl parks it",
		s.Active[2] <= retMap(s)[10] && len(u.Reclaim()) == 0)
	u.AdvanceEpoch()
	gs := u.Snapshot() // rule ep<G would be 0<1 true, yet T2 is still reading
	report("rule 'ep < G' frees A prematurely; min-active rule keeps it",
		retMap(gs)[10] < gs.G && len(u.Reclaim()) == 0)
	leak := true
	for k := 0; k < 5; k++ {
		u.AdvanceEpoch()
		if len(u.Reclaim()) != 0 {
			leak = false
		}
	}
	_ = u.Exit(2)
	report("permanent reader at ep0 leaks A; it frees after the reader exits",
		leak && reflect.DeepEqual(u.Reclaim(), []int{10}))
	e := api.New()
	_ = e.Enter(1)
	_ = e.Retire(10)
	got := []error{e.Enter(1), e.Exit(9), e.Retire(0), e.Retire(10)}
	want := []error{api.ErrDuplicateEnter, api.ErrNotActive, api.ErrInvalidNode, api.ErrAlreadyRetired}
	distinct := true
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			distinct = false
		}
	}
	report("four rejected ops return four distinct judgeable errors", distinct)
	before := e.Snapshot()
	_ = e.Enter(1)
	_ = e.Exit(9)
	_ = e.Retire(0)
	_ = e.Retire(10)
	report("rejected ops leave G/active/retired unchanged and manager usable",
		reflect.DeepEqual(before, e.Snapshot()) && e.Retire(11) == nil)
	big := api.New()
	for i := 0; i < 10000; i++ {
		_ = big.Enter(i)
	}
	_ = big.Retire(50001)
	report("m=10000 readers: none freed; min-active is O(1) (counter pinned by white-box test)",
		len(big.Reclaim()) == 0)
	const N = 64
	c := api.New()
	var ann [N]atomic.Int64
	var rep [N + 1]atomic.Int64
	var once [N + 1]atomic.Bool
	var freed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = c.Enter(i)
			ann[i].Store(c.Snapshot().Active[i] + 1)
			_ = c.Retire(i + 1)
			for _, it := range c.Snapshot().Retired {
				if it.Node == i+1 {
					rep[i+1].Store(it.Epoch)
				}
			}
			_ = c.Exit(i)
			ann[i].Store(0)
		}(i)
	}
	safe := true
	for freed.Load() < N {
		c.AdvanceEpoch()
		for _, id := range c.Reclaim() {
			for j := 0; j < N; j++ {
				if a := ann[j].Load(); a > 0 && a-1 <= rep[id].Load() {
					safe = false
				}
			}
			if once[id].Swap(true) {
				safe = false
			}
			freed.Add(1)
		}
		runtime.Gosched()
	}
	wg.Wait()
	report("concurrent: each node freed exactly once, never under a live reader", safe && freed.Load() == N)
	if fails > 0 {
		panic("demo had failures")
	}
}
