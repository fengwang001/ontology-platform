package api_test

import (
	"errors"
	"math/rand"
	"ontology/api"
	"sync"
	"sync/atomic"
	"testing"
)

type row [3]int64

func refGet(t *testing.T, w *api.View, ref map[int]row) {
	for id, want := range ref {
		if a, b, c, err := w.Get(id); err != nil || (row{a, b, c}) != want {
			t.Fatalf("Get(%d) mismatch, want %v", id, want)
		}
	}
	if p, err := w.Project([]api.Column{api.A, api.B, api.C}); err != nil || len(p) != 3 || len(p[0]) != len(ref) {
		t.Fatalf("Project misaligned: %v", err)
	}
}

func TestReferenceModel(t *testing.T) {
	for _, ops := range []int{50, 500, 5000} {
		rng := rand.New(rand.NewSource(int64(ops)))
		w, ref, live := api.New(), map[int]row{}, []int(nil)
		for i := 0; i < ops; i++ {
			switch op := rng.Intn(4); {
			case op < 2 || len(live) == 0:
				r := row{rng.Int63(), rng.Int63(), rng.Int63()}
				id := w.Insert(r[0], r[1], r[2])
				ref[id], live = r, append(live, id)
			case op == 2:
				k, c := rng.Intn(len(live)), rng.Intn(3)
				w.Update(live[k], api.Column(c), int64(i))
				r := ref[live[k]]
				r[c] = int64(i)
				ref[live[k]] = r
			case op == 3:
				k := rng.Intn(len(live))
				w.Delete(live[k])
				delete(ref, live[k])
				live = append(live[:k], live[k+1:]...)
			}
		}
		w.Compact()
		refGet(t, w, ref)
	}
}
func TestStableRowIDAcrossCompact(t *testing.T) {
	for _, del := range [][]int{{0}, {1, 3}, {0, 1, 2, 3, 4}, {}} {
		w, ref := api.New(), map[int]row{}
		for i := 0; i < 6; i++ {
			r := row{int64(10 * i), int64(10*i + 1), int64(10*i + 2)}
			w.Insert(r[0], r[1], r[2])
			ref[i] = r
		}
		for _, d := range del {
			w.Delete(d)
			delete(ref, d)
		}
		w.Compact()
		refGet(t, w, ref)
		for _, d := range del {
			if _, _, _, err := w.Get(d); !errors.Is(err, api.ErrDeleted) {
				t.Fatalf("del=%v: deleted id err=%v", del, err)
			}
		}
	}
}

func TestProjectAlignment(t *testing.T) {
	w := api.New()
	for i := 0; i < 7; i++ {
		w.Insert(int64(i), int64(100+i), int64(200+i))
	}
	w.Delete(2)
	w.Delete(5)
	live := []int{0, 1, 3, 4, 6}
	subsets := [][]api.Column{{api.A}, {api.B}, {api.C}, {api.A, api.B}, {api.A, api.C}, {api.B, api.C}, {api.A, api.B, api.C}}
	for _, cols := range subsets {
		p, err := w.Project(cols)
		if err != nil || len(p) != len(cols) {
			t.Fatalf("Project(%v): %v", cols, err)
		}
		for j, col := range cols {
			for i, id := range live {
				if len(p[j]) != len(live) || p[j][i] != int64(int(col)*100+id) {
					t.Fatalf("Project(%v)[%d][%d] misaligned", cols, j, i)
				}
			}
		}
	}
}

func TestFailureNoTrace(t *testing.T) {
	w := api.New()
	id0 := w.Insert(1, 2, 3)
	w.Insert(4, 5, 6)
	w.Delete(id0)
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { _, _, _, e := w.Get(99); return e }, api.ErrNotFound},
		{func() error { _, _, _, e := w.Get(id0); return e }, api.ErrDeleted},
		{func() error { return w.Update(id0, api.B, 0) }, api.ErrDeleted},
		{func() error { _, e := w.Project(nil); return e }, api.ErrEmptyCols},
	}
	for _, tc := range cases {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Fatalf("err=%v, want %v", err, tc.want)
		}
	}
	if api.ErrNotFound == api.ErrDeleted || api.ErrDeleted == api.ErrEmptyCols || api.ErrNotFound == api.ErrEmptyCols {
		t.Fatal("sentinel errors not mutually distinct")
	}
	w.Insert(7, 8, 9)
	refGet(t, w, map[int]row{1: {4, 5, 6}, 2: {7, 8, 9}}) // untouched, still usable
}

func TestConcurrentReadOnly(t *testing.T) {
	w := api.New()
	for i := 0; i < 200; i++ {
		w.Insert(int64(i), int64(-i), int64(7*i))
	}
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for it := 0; it < 200; it++ {
				id := (g + it) % 200
				if a, b, c, err := w.Get(id); err != nil || a != int64(id) || b != int64(-id) || c != int64(7*id) {
					bad.Add(1)
				}
				if p, err := w.Project([]api.Column{api.B, api.C}); err != nil || p[0][id] != int64(-id) || p[1][id] != int64(7*id) {
					bad.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d mismatched reads", bad.Load())
	}
}
