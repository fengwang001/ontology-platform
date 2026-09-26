package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/ch"
	"ontology/sketch"
)

func fail(tag string, err error) {
	fmt.Printf("FAIL: %s: %v\n", tag, err)
	os.Exit(1)
}

func main() {
	// Section 3 worked example: Add(2,4) Add(5,2) Add(11,1), w=6,d=3.
	s, err := sketch.New(6, 3)
	if err != nil {
		fail("new", err)
	}
	for _, e := range [][2]int64{{2, 4}, {5, 2}, {11, 1}} {
		if err := s.Add(e[0], e[1]); err != nil {
			fail("add", err)
		}
	}
	cells := map[[2]int]int64{{1, 2}: 4, {1, 5}: 3, {2, 5}: 7, {3, 1}: 4, {3, 4}: 3}
	for c, want := range cells {
		if got := s.Cell(c[0], c[1]); got != want {
			fail("cells", fmt.Errorf("r%dc%d=%d want %d", c[0], c[1], got, want))
		}
	}
	for _, q := range [][2]int64{{2, 4}, {5, 3}, {11, 3}} {
		if got, err := s.Query(q[0]); err != nil || got != q[1] {
			fail("query", fmt.Errorf("Query(%d)=%d want %d", q[0], got, q[1]))
		}
	}
	fmt.Println("OK: worked example cells {r1c2:4,r1c5:3,r2c5:7,r3c1:4,r3c4:3}; Query(2/5/11)=4/3/3")
	a, err := api.New(256, 5)
	if err != nil {
		fail("api new", err)
	}
	exact := map[int64]int64{}
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 2000; i++ {
		k, c := rng.Int63n(400), rng.Int63n(9)+1
		if err := a.Add(k, c); err != nil {
			fail("api add", err)
		}
		exact[k] += c
	}
	for k, c := range exact {
		if g, _ := a.Query(k); g < c {
			fail("no-underestimate", fmt.Errorf("Query(%d)=%d<%d", k, g, c))
		}
	}
	cf, err := api.New(4096, 5)
	if err != nil {
		fail("api new cf", err)
	}
	fam, err := ch.NewFamily(4096)
	if err != nil {
		fail("ch family", err)
	}
	used := make([]map[int]bool, 5)
	for j := range used {
		used[j] = map[int]bool{}
	}
	cnt := 0
	for x := int64(0); cnt < 8; x++ {
		ok := true
		for j := 0; j < 5; j++ {
			if used[j][fam.Column(j+1, x)] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for j := 0; j < 5; j++ {
			used[j][fam.Column(j+1, x)] = true
		}
		if err := cf.Add(x, int64(7+cnt)); err != nil {
			fail("cf add", err)
		}
		if g, _ := cf.Query(x); g != int64(7+cnt) {
			fail("collision-free exact", fmt.Errorf("Query(%d)=%d want %d", x, g, 7+cnt))
		}
		cnt++
	}
	fmt.Println("OK: never underestimate vs exact map; single-key & collision-free exact")

	if _, err := api.New(0, 3); !errors.Is(err, api.ErrInvalidParams) {
		fail("err params", err)
	}
	if err := a.Add(-1, 1); !errors.Is(err, api.ErrInvalidKey) {
		fail("err key", err)
	}
	if err := a.Add(1, 0); !errors.Is(err, api.ErrInvalidCount) {
		fail("err count", err)
	}
	if errors.Is(api.ErrInvalidParams, api.ErrInvalidKey) || errors.Is(api.ErrInvalidCount, api.ErrInvalidKey) {
		fail("distinct", errors.New("sentinels not distinct"))
	}
	before, _ := a.Query(2)
	for _, e := range [][2]int64{{-9, 1}, {2, 0}, {2, -4}} {
		_ = a.Add(e[0], e[1])
	}
	if after, _ := a.Query(2); after != before {
		fail("no-trace", fmt.Errorf("%d -> %d", before, after))
	}
	fmt.Println("OK: three distinct sentinel errors; rejected ops leave state unchanged")
	if err := a.SelfCheck(); err != nil { // covers probes == d at m = 100/1000/10000
		fail("self-check", err)
	}
	fmt.Println("OK: SelfCheck (incl. Query touches exactly d cells at m=100/1000/10000)")

	keys := []int64{1, 2, 3, 40, 57, 200, 399}
	ref := make([]int64, len(keys))
	for i, k := range keys {
		ref[i], _ = a.Query(k)
	}
	const N = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	var bad error
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, k := range keys {
				if v, _ := a.Query(k); v != ref[i] {
					mu.Lock()
					bad = fmt.Errorf("Query(%d)=%d want %d", k, v, ref[i])
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if bad != nil {
		fail("concurrent", bad)
	}
	fmt.Println("OK: 16 goroutines concurrent Query agree key by key")
}
