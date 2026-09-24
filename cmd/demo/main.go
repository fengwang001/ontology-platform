// Command demo exercises the tiered state store and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/store"
	"ontology/tier"
)

var fails int

func ok(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%s: %s\n", name, status)
}

// snap renders one step as "hot{...}/d=reads".
func snap(s *store.Store, reads int) string {
	return fmt.Sprintf("hot{%s}/d=%d", strings.Join(s.HotKeys(), ","), reads)
}

func contains(ss []string, t string) bool {
	for _, q := range ss {
		if q == t {
			return true
		}
	}
	return false
}

func main() {
	// tier: timestamp ordering with lexicographic tie-break, O(1) victim.
	x := tier.NewIndex()
	x.Add(tier.Entry{Key: "a", Stamp: 1})
	x.Add(tier.Entry{Key: "b", Stamp: 1})
	v0, _ := x.Victim() // equal stamp -> smaller key "a" is older
	x.Touch("a", 2)
	v1, _ := x.Victim() // after touching a, back is "b"
	ok("tier lru order", v0.Key == "a" && v1.Key == "b" &&
		tier.Older(tier.Entry{Key: "a", Stamp: 1}, tier.Entry{Key: "b", Stamp: 1}) &&
		!tier.Older(tier.Entry{Key: "b", Stamp: 2}, tier.Entry{Key: "a", Stamp: 1}))

	// store: replay the seven NOTES.md operations at memCap=2.
	s := store.New(2)
	s.Write("A", "1")
	l1 := snap(s, s.DiskReads())
	s.Write("B", "2")
	l2 := snap(s, s.DiskReads())
	rA, _ := s.Read("A")
	l3 := snap(s, s.DiskReads())
	before := strings.Join(s.HotKeys(), ",")
	s.Write("C", "3")
	evicted := "A" // after step 3 hot is {B(oldest),A}; C dislodges B
	if strings.Contains(before, "B") && !contains(s.HotKeys(), "B") {
		evicted = "B"
	}
	l4 := snap(s, s.DiskReads())
	rB5, _ := s.Read("B")
	l5 := snap(s, s.DiskReads())
	s.Write("B", "20")
	l6 := snap(s, s.DiskReads())
	rB7, found7 := s.Read("B")
	l7 := snap(s, s.DiskReads())
	fmt.Printf("steps: 1[%s] 2[%s] 3[%s] 4[%s] 5[%s] 6[%s] 7[%s]\n", l1, l2, l3, l4, l5, l6, l7)
	ok("step4 evict=B, step5 R(B)=2 d=1, step7 R(B)=20",
		evicted == "B" && rA == "1" && rB5 == "2" && s.DiskReads() == 1 &&
			rB7 == "20" && found7)
	_, foundD := s.Read("D")
	ok("unwritten D -> not found, no state added", !foundD && !contains(s.HotKeys(), "D"))
	ok("eviction scan count is O(1) in m", store.ScanBoundVerified() == nil)

	// api: five distinct decidable sentinel errors; rejection leaves no trace.
	if _, err := api.New(0, 8, 4); !errors.Is(err, api.ErrInvalidCapacity) {
		fails++
	}
	a, _ := api.New(2, 8, 2)
	_ = a.Write("k1", "v1")
	_ = a.Write("k2", "v2")
	reads0 := a.DiskReads()
	sentinels := []error{api.ErrEmptyKey, api.ErrEmptyValue, api.ErrKeyTooLong, api.ErrTooManyKeys}
	bads := []func() error{
		func() error { return a.Write("", "v") },
		func() error { return a.Write("z", "") },
		func() error { return a.Write("toolongkey", "v") },
		func() error { return a.Write("k3", "v") },
	}
	distinct := true
	seen := map[error]bool{}
	for i, b := range bads {
		err := b()
		if !errors.Is(err, sentinels[i]) || seen[err] {
			distinct = false
		}
		seen[err] = true
	}
	av, aok := a.Read("k1")
	ok("api 5 distinct sentinels, state intact after reject",
		distinct && a.DiskReads() == reads0 && av == "v1" && aok && a.SelfCheck() == nil)

	// concurrency: N goroutines read the same keys behind one start barrier;
	// every goroutine must see identical per-key values.
	c, _ := api.New(4, 32, 16)
	const n = 8
	for i := 0; i < 12; i++ { // 12 keys, cap 4 -> most start cold on disk
		_ = c.Write(fmt.Sprintf("key%02d", i), fmt.Sprintf("val%02d", i))
	}
	var start, done sync.WaitGroup
	start.Add(1)
	results := make([]map[string]string, n)
	for g := 0; g < n; g++ {
		done.Add(1)
		go func(g int) {
			defer done.Done()
			start.Wait()
			m := map[string]string{}
			for i := 0; i < 12; i++ {
				k := fmt.Sprintf("key%02d", i)
				v, f := c.Read(k)
				if !f {
					m[k] = "<missing>"
				} else {
					m[k] = v
				}
			}
			results[g] = m
		}(g)
	}
	start.Done()
	done.Wait()
	agree := true
	for i := 1; i < n; i++ {
		if fmt.Sprint(results[i]) != fmt.Sprint(results[0]) {
			agree = false
		}
	}
	ok("concurrent readers agree per key", agree)
	if fails > 0 {
		os.Exit(1)
	}
}
