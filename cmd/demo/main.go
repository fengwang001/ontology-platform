// Command demo exercises the consistent-hash ring and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"

	"ontology/api"
	"ontology/hashk"
	"ontology/ring"
)

var failed bool

func check(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func naive(v int, nodes []uint32, key uint32) uint32 {
	type slot struct{ pos, node uint32 }
	var ss []slot
	for _, id := range nodes {
		for i := 0; i < v; i++ {
			ss = append(ss, slot{hashk.VNodePos(id, i), id})
		}
	}
	sort.Slice(ss, func(a, b int) bool { return ss[a].pos < ss[b].pos })
	h := hashk.H(key)
	for _, s := range ss {
		if s.pos >= h {
			return s.node
		}
	}
	return ss[0].node
}

func main() {
	const v = 2
	nodes := []uint32{1, 2, 3}
	r, _ := api.New(v)
	for _, id := range nodes {
		r.AddNode(id)
	}
	keys := []uint32{10, 20, 30, 40, 50, 60, 70, 80}
	want := []uint32{1, 2, 3, 1, 2, 1, 3, 3}
	got := make([]uint32, len(keys))
	for i, k := range keys {
		got[i], _ = r.Get(k)
	}
	check("eight keys ownership 1/2/3/1/2/1/3/3", eq(got, want))
	owner50, _ := r.Get(50)
	check("key 50 wraps around to node 2", owner50 == 2 && uint32(0xe6d5c492) == hashk.H(50))

	naiveOK := true
	for i, k := range keys {
		naiveOK = naiveOK && got[i] == naive(v, nodes, k)
	}
	check("Get matches naive sorted linear scan", naiveOK)

	r.RemoveNode(3)
	remap := true
	for k, w := range map[uint32]uint32{30: 1, 70: 2, 80: 1} {
		o, _ := r.Get(k)
		remap = remap && o == w
	}
	check("after RemoveNode(3): 30->1 70->2 80->1", remap)

	e, _ := api.New(2)
	_, errEmpty := e.Get(1)
	e.AddNode(1)
	errDup := e.AddNode(1)
	errMissing := e.RemoveNode(9)
	_, errVnodes := api.New(0)
	distinct := errors.Is(errEmpty, api.ErrEmptyRing) &&
		errors.Is(errDup, api.ErrNodeExists) &&
		errors.Is(errMissing, api.ErrNodeNotFound) &&
		errors.Is(errVnodes, api.ErrInvalidVnodes) &&
		errEmpty != errDup && errDup != errMissing && errMissing != errVnodes
	check("four distinct decidable sentinel errors", distinct)
	check("state unchanged after rejected ops; still usable",
		e.NodeCount() == 1 && e.AddNode(2) == nil && e.NodeCount() == 2)

	costOK := true
	for _, m := range []int{100, 1000, 5000, 10000} {
		lr := ring.New(1)
		for id := uint32(1); id <= uint32(m); id++ {
			lr.Add(id)
		}
		lr.Get(123456789)
		bound := int(math.Ceil(math.Log2(float64(m)))) + 2
		costOK = costOK && lr.LookupCostBounded(bound) && lr.LookupCostBounded(16)
	}
	check("Get comparisons bounded ~log2(m), not linear in m", costOK)

	cr, _ := api.New(3)
	for _, id := range []uint32{1, 2, 3, 4, 5, 6, 7, 8} {
		cr.AddNode(id)
	}
	ck := []uint32{10, 20, 30, 40, 50, 60, 70, 80, 256, 4000000000}
	cw := make([]uint32, len(ck))
	for i, k := range ck {
		cw[i], _ = cr.Get(k)
	}
	const n = 16
	var wg sync.WaitGroup
	raceOK := true
	var mu sync.Mutex
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, k := range ck {
				o, _ := cr.Get(k)
				if o != cw[i] || o != naive(3, []uint32{1, 2, 3, 4, 5, 6, 7, 8}, k) {
					mu.Lock()
					raceOK = false
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent reads mutually consistent and naive-correct", raceOK)

	if failed {
		os.Exit(1)
	}
}

func eq(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
