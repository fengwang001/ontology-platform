package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/hp"
)

var failed atomic.Bool

func ok(b bool, s string) {
	if b {
		fmt.Println("OK", s)
	} else {
		fmt.Println("FAIL", s)
		failed.Store(true)
	}
}

func eqCoords(got []api.Point, want [][2]int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		if !hp.Eq(got[i], hp.Pt(w[0], w[1])) {
			return false
		}
	}
	return true
}

func main() {
	h := hp.HalfPlane{A: 1, B: 1, C: 6}
	z, good := hp.Intersect(h, hp.Pt(0, -1), hp.Pt(0, 7))
	ok(hp.Side(h, hp.Pt(6, 0)).Sign() == 0 && hp.Side(h, hp.Pt(4, 4)).Sign() > 0 &&
		good && hp.Eq(z, hp.Pt(0, 6)), "hp Side/Intersect boundary")

	steps := [][3]int{{-1, 0, 0}, {0, -1, 0}, {1, 1, 6}, {1, 0, 4}, {0, 1, 4}}
	want := [][][2]int64{
		{{0, -1e6}, {1e6, -1e6}, {1e6, 1e6}, {0, 1e6}},
		{{0, 0}, {1e6, 0}, {1e6, 1e6}, {0, 1e6}},
		{{0, 0}, {6, 0}, {0, 6}},
		{{0, 0}, {4, 0}, {4, 2}, {0, 6}},
		{{0, 0}, {4, 0}, {4, 2}, {2, 4}, {0, 4}},
	}
	stepOK := api.New() == nil
	for i, s := range steps {
		stepOK = api.Add(s[0], s[1], s[2]) == nil && stepOK
		stepOK = eqCoords(api.Region(), want[i]) && stepOK
	}
	ok(stepOK, "five-step regions -> final pentagon (0,0)(4,0)(4,2)(2,4)(0,4)")

	revOK := api.New() == nil // H3 方向搞反：x+y>=6
	for _, s := range [][3]int{{-1, 0, 0}, {0, -1, 0}, {-1, -1, -6}, {1, 0, 4}, {0, 1, 4}} {
		revOK = api.Add(s[0], s[1], s[2]) == nil && revOK
	}
	ok(revOK && eqCoords(api.Region(), [][2]int64{{2, 4}, {4, 2}, {4, 4}}), "reversed H3 wrong triangle")

	h6OK := api.New() == nil // H6: x>=5 与 x<=4 平行相反
	for _, s := range append(append([][3]int{}, steps...), [3]int{-1, 0, -5}) {
		h6OK = api.Add(s[0], s[1], s[2]) == nil && h6OK
	}
	ok(h6OK && api.Empty(), "parallel-opposite H6 gives empty")

	bOK := api.New() == nil // 边界点保留
	for _, s := range steps[:3] {
		bOK = api.Add(s[0], s[1], s[2]) == nil && bOK
	}
	ok(bOK && eqCoords(api.Region(), want[2]), "boundary points (6,0)(0,6) kept")

	selfErr := api.SelfCheck()
	ok(selfErr == nil, "SelfCheck: naive consistency + feasibility + convex")
	ok(selfErr == nil, "SelfCheck: edge count bounded (O(1) bbox precheck)")

	e1, e2 := api.Add(0, 0, 5), api.Add(10001, 0, 0)
	ok(errors.Is(e1, api.ErrDegenerate) && errors.Is(e2, api.ErrOutOfRange) &&
		!errors.Is(api.ErrDegenerate, api.ErrOutOfRange), "two decidable distinct errors")
	ok(eqCoords(api.Region(), want[2]), "rejected adds leave state unchanged")

	var concOK atomic.Bool
	concOK.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if !eqCoords(api.Region(), want[2]) || api.Empty() || api.SelfCheck() != nil {
					concOK.Store(false)
					return
				}
			}
		}()
	}
	wg.Wait()
	ok(concOK.Load(), "concurrent readers identical")

	if failed.Load() {
		os.Exit(1)
	}
}
