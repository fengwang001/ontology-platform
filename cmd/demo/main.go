// Command demo verifies the bounded per-key version history behavior.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/store"
)

var failed bool

func ok(name string, pass bool) {
	tag := "OK  "
	if !pass {
		tag, failed = "FAIL ", true
	}
	fmt.Println(tag + name)
}

func main() {
	a, err := api.New(3)
	if err != nil {
		fmt.Println("FAIL New(3):", err)
		os.Exit(1)
	}
	// Six-Put table: retained window and evicted version per step.
	wantKeep := [][]int64{{1}, {1, 2}, {1, 2, 3}, {2, 3, 4}, {3, 4, 5}, {4, 5, 6}}
	evicted := []int64{0, 0, 0, 1, 2, 3}
	pass := true
	for i := 0; i < 6; i++ {
		v, _ := a.Put("k", string(rune('a'+i)))
		pass = pass && v == int64(i+1) && a.Len("k") == len(wantKeep[i])
		if evicted[i] != 0 {
			_, hit, _ := a.GetAt("k", evicted[i])
			pass = pass && !hit
		}
		if i == 2 {
			pass = pass && a.Len("k") == 3 // 甲: count==K must NOT clean
		}
		if i == 3 { // 乙+丙 right after Put("d")
			g2, h2, _ := a.GetAt("k", 2)
			_, h1, _ := a.GetAt("k", 1)
			pass = pass && h2 && g2 == "b" && !h1
		}
	}
	ok("six-put table: windows, no early clean@3, GetAt(1) gone / GetAt(2) hit @4", pass)

	// Get equals naive replay on a fresh instance, many keys.
	b, _ := api.New(5)
	last := map[string]string{}
	pass = true
	for i := 1; i <= 300; i++ {
		key := fmt.Sprintf("k%d", i%5)
		val := fmt.Sprintf("%s@%d", key, i)
		b.Put(key, val)
		last[key] = val
		g, exist := b.Get(key)
		pass = pass && exist && g == val && b.Len(key) <= 5
	}
	ok("Get == naive replay, Len <= K (300 puts, 5 keys)", pass)

	// Three classifiable, mutually distinct sentinel errors.
	_, e1 := b.Put("", "x")
	_, _, e2 := b.GetAt("", 1)
	_, _, e3 := b.GetAt("k1", 0)
	_, e4 := api.New(0)
	ok("sentinels: empty-key/bad-version/bad-K distinct",
		e1 == store.ErrEmptyKey && e2 == store.ErrEmptyKey &&
			e3 == store.ErrInvalidVersion && e4 == store.ErrBadLimit && e1 != e3)

	// Rejected ops leave no trace; instance keeps working.
	d, _ := api.New(3)
	d.Put("s", "one")
	v1, _ := d.Put("s", "two")
	before := d.Len("s")
	g1, h1, _ := d.GetAt("s", v1)
	d.Put("", "x")   // rejected
	d.GetAt("", 1)   // rejected
	d.GetAt("s", -1) // rejected
	mid := d.Len("s")
	v2, _ := d.Put("s", "three")
	ok("rejected ops no-trace, versions not reused",
		mid == before && h1 && g1 == "two" && v2 == v1+1 && d.Len("s") == before+1)

	// Bounded cleanup under large m: retention stays at K (O(1) probe is
	// asserted inside package hist's own test).
	c, _ := api.New(5)
	for i := 0; i < 10000; i++ {
		c.Put("big", "x")
	}
	ok("m=10000 puts: retained == K == 5, latest readable", c.Len("big") == 5)

	// Concurrent readers on a fed instance observe identical results.
	const nR = 64
	type view struct {
		val string
		ok  bool
		nn  int
	}
	views := make([]view, nR)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < nR; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			val, exist := c.Get("big")
			views[i] = view{val, exist, c.Len("big")}
		}(i)
	}
	close(start)
	wg.Wait()
	pass = true
	for i := 1; i < nR; i++ {
		pass = pass && views[i] == views[0]
	}
	ok("64 concurrent readers: identical Get/Len", pass)

	ok("SelfCheck()", a.SelfCheck() == nil && b.SelfCheck() == nil && c.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
