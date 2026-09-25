// Command demo exercises the broadcast map-side join packages and prints
// one OK/FAIL per check. Exit code 0 means every check passed. No args, no
// network. Output stays within 10 lines.
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/dim"
)

func main() {
	fail := false
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
		} else {
			fmt.Println("FAIL " + name)
			fail = true
		}
	}

	// White-box dim replay d and end-to-end api replay a stay in lockstep
	// with the eight spec operations; d pins V / used / versions.
	d := dim.New(100)
	a, _ := api.New(100)
	b1 := []dim.Entry{{Key: "a", Val: 1}, {Key: "b", Val: 2}}
	b2 := []dim.Entry{{Key: "c", Val: 3}, {Key: "d", Val: 4}}
	b4 := []dim.Entry{{Key: "a", Val: 9}}
	b7 := []dim.Entry{{Key: "e", Val: 5}, {Key: "f", Val: 6}}
	cast := func(b []dim.Entry) {
		d.Broadcast(b)
		a.Broadcast([]api.Entry{{Key: b[0].Key, Val: b[0].Val}, {Key: b[1].Key, Val: b[1].Val}})
	}
	castOne := func(b []dim.Entry) { d.Broadcast(b); a.Broadcast(b) }

	cast(b1) // step 1
	cast(b2) // step 2
	check("steps1-2 broadcast: V=2 used=54 versions=[0 1 2]",
		d.V() == 2 && d.Used() == 54 && reflect.DeepEqual(d.Versions(), []int64{0, 1, 2}))
	r3, _ := a.Join(api.Fact{Key: "a", Vsn: 1}) // step 3
	check("step3 Fact a@1 -> (a,1,1)", r3 == (api.Row{Key: "a", Val: 1, Vsn: 1}))

	castOne(b4) // step 4
	check("step4 broadcast: V=3 used=90 versions=[0 1 2 3]",
		d.V() == 3 && d.Used() == 90 && reflect.DeepEqual(d.Versions(), []int64{0, 1, 2, 3}))
	r5, _ := a.Join(api.Fact{Key: "a", Vsn: 1}) // step 5
	r6, _ := a.Join(api.Fact{Key: "a", Vsn: 3}) // step 6
	check("step5 a@1 -> (a,1,1) [not current 9]; step6 a@3 -> (a,9,3)",
		r5 == (api.Row{Key: "a", Val: 1, Vsn: 1}) && r6 == (api.Row{Key: "a", Val: 9, Vsn: 3}))

	cast(b7) // step 7
	check("step7 eviction: versions=[3 4] used=90 oldest=3; e@4=(e,5,4)",
		reflect.DeepEqual(d.Versions(), []int64{3, 4}) && d.Used() == 90 && d.Oldest() == 3)
	r8, _ := a.Join(api.Fact{Key: "a", Vsn: 1})   // step 8: stale
	re4, e8 := a.Join(api.Fact{Key: "e", Vsn: 4}) // e present in v4
	check("step8 a@1 stale (no row, dropped=1); e@4 found -> (e,5,4)",
		r8 == api.Row{} && a.Dropped() == 1 && a.Missed() == 0 &&
			e8 == nil && re4 == (api.Row{Key: "e", Val: 5, Vsn: 4}))

	before := []any{a.Joined(), a.Dropped(), a.Missed()}
	errs := []error{
		func() (e error) { _, e = a.Broadcast(nil); return }(),
		func() (e error) { _, e = a.Broadcast([]api.Entry{{Key: ""}}); return }(),
		func() (e error) { _, e = a.Join(api.Fact{Key: "a", Vsn: 99}); return }(),
		func() (e error) { _, e = api.New(0); return }(),
	}
	uniq := map[error]bool{}
	for _, e := range errs {
		uniq[e] = true
	}
	after := []any{a.Joined(), a.Dropped(), a.Missed()}
	check("four distinct sentinels (emptyBatch/emptyKey/future/badCap); rejected ops leave no trace",
		len(uniq) == 4 && reflect.DeepEqual(before, after))

	// probes is unexported: its O(1) claim is pinned by dim white-box
	// TestProbeCountConstant; externally we verify correctness at every m.
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		b, _ := api.New(1 << 30)
		es := make([]api.Entry, m)
		for i := range es {
			es[i] = api.Entry{Key: fmt.Sprintf("k%05d", i), Val: int64(i)}
		}
		v, _ := b.Broadcast(es)
		r, e := b.Join(api.Fact{Key: fmt.Sprintf("k%05d", m-1), Vsn: v})
		bigOK = bigOK && e == nil && r.Val == int64(m-1)
	}
	check("big-m joins correct at m=100..10000; probe-count O(1) pinned by TestProbeCountConstant", bigOK)

	gots := make([][]api.Row, 16)
	var wg sync.WaitGroup
	for i := range gots {
		wg.Add(1)
		go func(i int) { defer wg.Done(); gots[i] = a.Joined() }(i)
	}
	wg.Wait()
	same := true
	for _, g := range gots {
		same = same && reflect.DeepEqual(g, a.Joined())
	}
	check("concurrent Joined identical across 16 goroutines; SelfCheck=ok", same && a.SelfCheck() == nil)

	if fail {
		os.Exit(1)
	}
}
