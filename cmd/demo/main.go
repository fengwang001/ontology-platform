package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/col"
	"ontology/view"
)

var failed bool

func report(ok bool, name string) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

type row [3]int64

func projEquals(w *api.View, rows []row) bool {
	p, err := w.Project([]api.Column{api.A, api.B, api.C})
	if err != nil || len(p) != 3 {
		return false
	}
	for i := range p {
		if len(p[i]) != len(rows) {
			return false
		}
	}
	for i, r := range rows {
		if p[0][i] != r[0] || p[1][i] != r[1] || p[2][i] != r[2] {
			return false
		}
	}
	return true
}

func main() {
	s := &col.Store{}
	s.Insert(0, 10, 20, 30)
	s.Insert(1, 1, 2, 3)
	ok := s.Delete(0) == nil
	s.Compact()
	a, b, c, err := s.Get(1)
	ok = ok && err == nil && a == 1 && b == 2 && c == 3
	_, _, _, err = s.Get(0)
	ok = ok && err == col.ErrDeleted && col.GetCostOK()
	report(ok, "col: compact keeps rowID bindings, get cost O(1) in m")

	v := view.New()
	v.Insert(10, 20, 30)
	id1 := v.Insert(1, 2, 3)
	v.Insert(100, 200, 300)
	ok = v.Delete(id1) == nil
	p, err := v.Project([]view.Column{view.B})
	ok = ok && err == nil && len(p) == 1 && len(p[0]) == 2 && p[0][0] == 20 && p[0][1] == 200
	_, err = v.Project(nil)
	ok = ok && err == view.ErrEmptyCols
	report(ok, "view: project skips tombstone, empty col set rejected")

	// The eight-step sequence from NOTES.md, checked after every step.
	w := api.New()
	ids := []int{w.Insert(10, 20, 30)}
	ok = projEquals(w, []row{{10, 20, 30}})
	ids = append(ids, w.Insert(1, 2, 3))
	ok = ok && projEquals(w, []row{{10, 20, 30}, {1, 2, 3}})
	ids = append(ids, w.Insert(100, 200, 300))
	ok = ok && projEquals(w, []row{{10, 20, 30}, {1, 2, 3}, {100, 200, 300}})
	ok = ok && w.Delete(ids[1]) == nil
	ok = ok && projEquals(w, []row{{10, 20, 30}, {100, 200, 300}})
	ids = append(ids, w.Insert(7, 8, 9))
	ok = ok && projEquals(w, []row{{10, 20, 30}, {100, 200, 300}, {7, 8, 9}})
	w.Compact()
	ok = ok && projEquals(w, []row{{10, 20, 30}, {100, 200, 300}, {7, 8, 9}})
	ok = ok && w.Update(ids[2], api.C, 999) == nil
	ok = ok && projEquals(w, []row{{10, 20, 30}, {100, 200, 999}, {7, 8, 9}})
	a, b, c, err = w.Get(ids[2])
	ok = ok && err == nil && a == 100 && b == 200 && c == 999
	report(ok, "api: 8-step trace matches NOTES.md, Get(rowID2)=(100,200,999)")

	_, _, _, err = w.Get(ids[1])
	report(err == api.ErrDeleted, "api: Get on deleted rowID reports ErrDeleted")

	ok = api.ErrNotFound != api.ErrDeleted && api.ErrDeleted != api.ErrEmptyCols &&
		api.ErrNotFound != api.ErrEmptyCols
	report(ok, "api: three sentinel errors are mutually distinct")

	before := []row{{10, 20, 30}, {100, 200, 999}, {7, 8, 9}}
	w.Get(1 << 30)
	w.Update(ids[1], api.A, 0)
	w.Project(nil)
	report(projEquals(w, before), "api: rejected ops leave state unchanged")

	const n = 300
	w2 := api.New()
	for i := 0; i < n; i++ {
		w2.Insert(int64(i), int64(2*i), int64(3*i))
	}
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for it := 0; it < 100; it++ {
				id := (g*100 + it) % n
				a, b, c, err := w2.Get(id)
				if err != nil || a != int64(id) || b != int64(2*id) || c != int64(3*id) {
					bad.Store(true)
				}
				p, err := w2.Project([]api.Column{api.A, api.C})
				if err != nil || len(p) != 2 || len(p[0]) != n ||
					p[0][id] != int64(id) || p[1][id] != int64(3*id) {
					bad.Store(true)
				}
			}
		}(g)
	}
	wg.Wait()
	report(!bad.Load(), "api: concurrent read-only Get/Project identical")

	report(api.SelfCheck() == nil, "api: SelfCheck verifies all four invariants")

	if failed {
		os.Exit(1)
	}
}
