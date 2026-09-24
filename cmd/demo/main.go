// Command demo exercises the tiered state store and prints OK/FAIL
// lines. It takes no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/store"
	"ontology/tier"
)

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	// tier: heap root is the smallest (At, Key) — the LRU victim.
	h := tier.NewHeap()
	h.Add(&tier.Entry{Key: "A", At: 3})
	h.Add(&tier.Entry{Key: "B", At: 2})
	check("tier: heap root is oldest B", h.Pop().Key == "B")

	// store: the mandated seven-step trace (hot set | diskReads per step).
	s, _ := store.NewStore(2)
	ops := []string{"wA1", "wB2", "rA1", "wC3", "rB2", "wB20", "rB20"}
	hot := [][]string{{"A"}, {"A", "B"}, {"A", "B"}, {"A", "C"}, {"B", "C"}, {"B", "C"}, {"B", "C"}}
	drs := []int{0, 0, 0, 0, 1, 1, 1}
	trace, ok := "", true
	for i, op := range ops {
		if op[0] == 'w' {
			s.Write(op[1:2], op[2:])
		} else if v, found := s.Read(op[1:2]); !found || v != op[2:] {
			ok = false
		}
		if !reflect.DeepEqual(s.HotKeys(), hot[i]) || s.DiskReads() != drs[i] {
			ok = false
		}
		trace += fmt.Sprintf("%d:%v|%d ", i+1, s.HotKeys(), s.DiskReads())
	}
	check("7-step hot|dr ["+trace+"]", ok)
	v7, _ := s.Read("B")
	_, foundD := s.Read("D")
	check("step4 evicts B; step7 Read(B)=20; Read(D) absent", v7 == "20" && !foundD)

	// api: five distinct, decidable sentinel errors.
	a, _ := api.New(2, 4, 2)
	sentinels := []error{api.ErrEmptyKey, api.ErrEmptyValue, api.ErrKeyTooLong, api.ErrTooManyKeys}
	attempts := []func() error{
		func() error { return a.Write("", "v") },
		func() error { return a.Write("k", "") },
		func() error { return a.Write("toolong", "v") },
		func() error { _ = a.Write("a", "1"); _ = a.Write("b", "1"); return a.Write("c", "1") },
	}
	distinct := true
	for i, attempt := range attempts {
		if !errors.Is(attempt(), sentinels[i]) {
			distinct = false
		}
	}
	_, badCap := api.New(0, 4, 2)
	check("five distinct decidable errors", distinct && errors.Is(badCap, api.ErrBadMemCap))

	// rejected op leaves no trace; store stays usable.
	b, _ := api.New(2, 4, 2)
	_ = b.Write("a", "1")
	beforeReads := b.DiskReads()
	rejected := errors.Is(b.Write("", "x"), api.ErrEmptyKey)
	still, stillOK := b.Read("a")
	check("rejection leaves state unchanged; still usable", rejected && stillOK && still == "1" && b.DiskReads() == beforeReads)

	// large-m constant probe + full invariants are verified inside SelfCheck
	// (pass/fail only; the counter value is never exposed).
	sc, _ := api.New(4, 16, 64)
	check("large-m eviction probes O(1); SelfCheck passes", sc.SelfCheck() == nil)

	// concurrency: N goroutines read only the resident set.
	c, _ := api.New(32, 16, 64)
	want := map[string]string{}
	for i := 0; i < 32; i++ {
		k := fmt.Sprintf("k%02d", i)
		_ = c.Write(k, fmt.Sprintf("v%d", i))
		want[k] = fmt.Sprintf("v%d", i)
	}
	const n = 16
	start, wg := make(chan struct{}), sync.WaitGroup{}
	got := make([]map[string]string, n)
	reads := make([]int, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			got[g] = map[string]string{}
			for r := 0; r < 100; r++ {
				for k, wv := range want {
					if v, ok := c.Read(k); !ok || v != wv {
						got[g][k] = "MISMATCH"
					} else {
						got[g][k] = v
					}
				}
			}
			reads[g] = c.DiskReads()
		}(g)
	}
	close(start)
	wg.Wait()
	consistent := true
	for g := 1; g < n; g++ {
		if !reflect.DeepEqual(got[g], want) || reads[g] != reads[0] {
			consistent = false
		}
	}
	check("concurrent readers agree per-key and on DiskReads", consistent && reflect.DeepEqual(got[0], want))
}
