package face_test

import (
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/face"
	"ontology/pg"
)

var (
	s3e = [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {0, 3}}
	s3r = [][]int{{1, 2, 3}, {0, 3, 2}, {0, 1}, {1, 0}}
	djE = [][2]int{{0, 1}, {1, 2}, {0, 2}, {3, 4}, {4, 5}, {3, 5}}
	djR = [][]int{{1, 2}, {0, 2}, {1, 0}, {4, 5}, {3, 5}, {4, 3}}
)

func setup(n int, es [][2]int, rs [][]int) *api.API {
	a, _ := api.New(n)
	for _, e := range es {
		a.AddEdge(e[0], e[1])
	}
	for v, r := range rs {
		a.SetRotation(v, r)
	}
	a.Compute()
	return a
}

func naive(es [][2]int, rs [][]int) int {
	vis, c := map[[2]int]bool{}, 0
	var walk func(int, int)
	walk = func(u, v int) {
		if vis[[2]int{u, v}] {
			return
		}
		c++
		for !vis[[2]int{u, v}] {
			vis[[2]int{u, v}] = true
			r := rs[v]
			i := slices.Index(r, u)
			u, v = v, r[(i-1+len(r))%len(r)]
		}
	}
	for _, e := range es {
		walk(e[0], e[1])
		walk(e[1], e[0])
	}
	return c
}
func TestFaceCoverage(t *testing.T) {
	a := setup(4, s3e, s3r)
	total := 0
	for _, f := range a.Faces() {
		total += len(f)
	}
	want := [][]int{{1, 0, 3}, {0, 2, 1, 3}, {0, 1, 2}}
	if total != 2*a.EdgeCount() || !reflect.DeepEqual(a.Faces(), want) {
		t.Fatalf("total=%d faces=%v", total, a.Faces())
	}
}
func TestNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		es := append([][2]int(nil), s3e...)
		rng.Shuffle(len(es), func(p, q int) { es[p], es[q] = es[q], es[p] })
		if naive(es, s3r) != setup(4, es, s3r).FaceCount() {
			t.Fatalf("iter %d mismatch", i)
		}
	}
}
func TestEuler(t *testing.T) {
	for _, c := range []struct {
		n, f int
		e    [][2]int
		r    [][]int
	}{
		{4, 3, s3e, s3r},
		{6, 3, djE, djR},
		{3, 1, [][2]int{{0, 1}}, [][]int{{1}, {0}, nil}},
	} {
		a := setup(c.n, c.e, c.r)
		if a.FaceCount() != c.f || !a.EulerHolds() {
			t.Fatalf("F=%d want %d euler=%v", a.FaceCount(), c.f, a.EulerHolds())
		}
	}
	if a, _ := api.New(1); a.SelfCheck() != nil {
		t.Fatal("SelfCheck failed")
	}
}
func TestProbeO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		g, _ := pg.New(m + 1)
		ord := make([]int, m)
		for i := 1; i <= m; i++ {
			g.AddEdge(0, i)
			ord[i-1] = i
			g.SetRotation(i, []int{0})
		}
		g.SetRotation(0, ord)
		tr, err := face.New(g.Snapshot())
		if err != nil || !tr.PrevProbeConstant() {
			t.Fatalf("m=%d probe not O(1): %v", m, err)
		}
	}
}
func TestRejectionLeavesState(t *testing.T) {
	a := setup(4, [][2]int{{0, 1}}, [][]int{{1}, {0}, nil, nil})
	_, e0 := api.New(0)
	cs := []error{e0, a.AddEdge(0, 4), a.AddEdge(1, 1), a.AddEdge(0, 1),
		a.SetRotation(0, []int{2}), a.SetRotation(0, []int{1, 1}), a.SetRotation(0, []int{1, 2})}
	ws := []error{api.ErrBadN, api.ErrBadVertex, api.ErrSelfLoop, api.ErrDuplicate,
		api.ErrBadRotation, api.ErrBadRotation, api.ErrBadRotation}
	for i := range cs {
		if cs[i] != ws[i] {
			t.Fatalf("case %d got %v want %v", i, cs[i], ws[i])
		}
	}
	if a.EdgeCount() != 1 {
		t.Fatal("state changed after rejects")
	}
	a.AddEdge(2, 3)
	for v, r := range [][]int{{1}, {0}, {3}, {2}} {
		a.SetRotation(v, r)
	}
	if err := a.Compute(); err != nil || !a.EulerHolds() {
		t.Fatal("graph unusable after rejects")
	}
}
func TestConcurrentRead(t *testing.T) {
	a := setup(4, s3e, s3r)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a.FaceCount() != 3 || !a.EulerHolds() {
				t.Error("inconsistent concurrent read")
			}
		}()
	}
	close(start)
	wg.Wait()
}
