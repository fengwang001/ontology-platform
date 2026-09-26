// Command demo verifies the Voronoi nearest-site implementation with a fixed,
// human-readable checklist. No arguments, no network I/O; exit code 0 on pass.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"

	"ontology/api"
	"ontology/nbr"
	"ontology/sites"
)

func main() {
	failed := false
	check := func(name, detail string, ok bool) {
		tag := "OK  "
		if !ok {
			tag, failed = "FAIL", true
		}
		fmt.Printf("%s %s — %s\n", tag, name, detail)
	}

	// Mandated eight-step sequence.
	_ = api.New()
	res := make([]int, 0, 8)
	do := func(v int, err error) { res = append(res, v) }
	do(api.Add(0, 0))
	do(api.Add(1, 1))
	do(api.Add(4, 0))
	do(api.Nearest(0, 2))
	do(api.Nearest(1, 0))
	do(api.Add(0, 2))
	do(api.Nearest(0, 2))
	do(api.Within(1, 0, 1))
	check("eight steps", fmt.Sprintf("results=%v", res),
		fmt.Sprint(res) == "[0 1 2 1 0 3 3 0]")

	n4 := res[3]
	check("step4 nearest=1", "manhattan distance would wrongly give 0", n4 == 1)
	n5 := res[4]
	check("step5 tie -> 0", "a <= scan would wrongly give 1", n5 == 0)
	w8 := res[7]
	check("step8 within=0", "d2<=r2 would wrongly give 1 (site 0 on circle)", w8 == 0)

	// Naive-scan consistency over random sets and queries.
	rng := rand.New(rand.NewSource(768))
	consistent := true
	for iter := 0; iter < 50; iter++ {
		s := sites.New()
		var pts []nbr.Point
		used := map[nbr.Point]struct{}{}
		for len(pts) < 60 {
			p := nbr.Point{X: rng.Intn(401) - 200, Y: rng.Intn(401) - 200}
			if _, dup := used[p]; dup {
				continue
			}
			used[p] = struct{}{}
			if _, err := s.Add(p); err == nil {
				pts = append(pts, p)
			}
		}
		for j := 0; j < 30; j++ {
			q := nbr.Point{X: rng.Intn(501) - 250, Y: rng.Intn(501) - 250}
			got, _ := s.Nearest(q)
			best, bi := int64(math.MaxInt64), -1
			for i, p := range pts {
				if d := nbr.Dist2(p, q); d < best {
					best, bi = d, i
				}
			}
			if got != bi {
				consistent = false
			}
		}
	}
	check("naive-scan consistency", "50 random sets x 30 queries", consistent)

	// Tie stability: sites 0 and 1 tie at d2=1; result must always be 0.
	stable := true
	for i := 0; i < 50; i++ {
		if v, _ := api.Nearest(1, 0); v != 0 {
			stable = false
		}
	}
	check("tie stability", "repeated tied query always returns index 0", stable)

	// Three mutually distinct, judgeable sentinel errors.
	_, eOOB := api.Add(10001, 0)
	_, eDup := api.Add(0, 0)
	_, eNeg := api.Within(0, 0, -1)
	distinct := errors.Is(eOOB, api.ErrOutOfBounds) && errors.Is(eDup, api.ErrDuplicate) &&
		errors.Is(eNeg, api.ErrNegativeRadius) && eOOB != eDup && eDup != eNeg && eOOB != eNeg
	check("three sentinel errors", "out-of-bounds / duplicate / negative-radius distinct", distinct)

	// Rejected operations leave no trace: next valid Add must be index 4.
	idx, errAdd := api.Add(9, 9)
	check("rejections leave no trace", fmt.Sprintf("next Add index=%d", idx), errAdd == nil && idx == 4)

	check("sublinear candidates", "m=100..10000, measured count stays constant", sites.Sublinear())

	// Concurrent read-only: every goroutine must see field-identical results.
	const nG = 16
	var wg sync.WaitGroup
	found := make([][4]int, nG)
	for g := 0; g < nG; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, q := range [4][2]int{{123, -456}, {0, 0}, {-8000, 8000}, {4500, 4500}} {
				v, err := api.Nearest(q[0], q[1])
				if err != nil {
					return
				}
				found[g][i] = v
			}
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < nG; g++ {
		if found[g] != found[0] {
			same = false
		}
	}
	check("concurrent read-only", "16 goroutines see identical nearest indices", same)

	if failed {
		panic("demo checks failed")
	}
}
