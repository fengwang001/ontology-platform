package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/lww"
	"ontology/omap"
)

func op(o, i string, v, ts int64, r string) omap.Op {
	return omap.Op{Kind: "put", O: o, I: i, V: v, TS: ts, Rep: r}
}
func delop(o string, ts int64) omap.Op { return omap.Op{Kind: "del", O: o, TS: ts} }
func must(t *testing.T, a *API, qs ...omap.Op) {
	t.Helper()
	for _, q := range qs {
		if err := a.Apply(q); err != nil {
			t.Fatal(err)
		}
	}
}
func TestBatchRecompute(t *testing.T) {
	A, B := New(), New()
	xs := [2]*API{A, B}
	var all []omap.Op
	ev := []struct {
		t int // 0=A 1=B -1=双向合并
		q omap.Op
	}{
		{0, op("o", "k1", 100, 1, "A")}, {0, op("o", "k2", 200, 2, "A")},
		{0, delop("o", 3)}, {1, op("o", "k3", 300, 1, "B")},
		{1, op("o", "k4", 400, 2, "B")}, {-1, omap.Op{}},
		{0, op("o", "k5", 500, 3, "C")}, {0, op("o", "k6", 600, 4, "D")},
	}
	for i, e := range ev {
		if e.t < 0 {
			A.Merge(B)
			B.Merge(A)
			w := batch(all)
			if !reflect.DeepEqual(A.View(), w) || !reflect.DeepEqual(B.View(), w) {
				t.Fatalf("ev%d A=%v B=%v want=%v", i, A.View(), B.View(), w)
			}
			continue
		}
		must(t, xs[e.t], e.q)
		all = append(all, e.q)
	}
}
func TestTombstonePropagation(t *testing.T) {
	cases := []struct {
		a, b   []omap.Op
		hidden []string
	}{
		{[]omap.Op{op("o", "k1", 1, 1, "A"), delop("o", 3)},
			[]omap.Op{op("o", "k3", 3, 1, "B"), op("o", "k4", 4, 2, "B")}, []string{"k1", "k3", "k4"}},
		{[]omap.Op{op("o", "k1", 1, 5, "A")}, []omap.Op{delop("o", 10)}, []string{"k1"}},
		{[]omap.Op{delop("o", 7)}, []omap.Op{delop("o", 4), op("o", "k", 1, 6, "B")}, []string{"k"}},
	}
	for ci, c := range cases {
		A, B := New(), New()
		must(t, A, c.a...)
		must(t, B, c.b...)
		A.Merge(B)
		B.Merge(A)
		for _, x := range []*API{A, B} {
			if _, vis := x.View()["o"]; vis {
				t.Fatalf("case%d o visible %v", ci, x.View())
			}
			for _, k := range c.hidden {
				if _, ok := x.View()["o"][k]; ok {
					t.Fatalf("case%d %s visible", ci, k)
				}
			}
		}
	}
}
func TestLWWConvergence(t *testing.T) {
	cases := []struct {
		a, b []omap.Op
		want int64
	}{
		{[]omap.Op{op("o", "k", 100, 5, "A")}, []omap.Op{op("o", "k", 999, 2, "B")}, 100},
		{[]omap.Op{op("o", "k", 100, 2, "A")}, []omap.Op{op("o", "k", 999, 5, "B")}, 999},
		{[]omap.Op{op("o", "k", 1, 5, "A")}, []omap.Op{op("o", "k", 2, 5, "B")}, 2},
	}
	for ci, c := range cases {
		A, B := New(), New()
		must(t, A, c.a...)
		must(t, B, c.b...)
		A.Merge(B)
		B.Merge(A)
		if A.View()["o"]["k"] != c.want || !reflect.DeepEqual(A.View(), B.View()) {
			t.Fatalf("case%d A=%v B=%v want %d", ci, A.View(), B.View(), c.want)
		}
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	cases := []struct {
		q omap.Op
		e error
	}{
		{op("", "i", 1, 1, "R"), lww.ErrEmptyOuter},
		{op("o", "", 1, 1, "R"), lww.ErrEmptyInner},
		{op("o", "i", 1, 0, "R"), lww.ErrNonPositiveTS},
	}
	for _, c := range cases {
		a := New()
		if err := a.Apply(c.q); !errors.Is(err, c.e) {
			t.Fatalf("err=%v want=%v", err, c.e)
		}
		must(t, a, op("o", "i", 7, 1, "R"))
		if len(a.View()) != 1 || a.View()["o"]["i"] != 7 {
			t.Fatalf("trace or unusable: %v", a.View())
		}
	}
}
func TestConcurrentViewEqual(t *testing.T) {
	a := New()
	for j := 0; j < 200; j++ {
		must(t, a, op(fmt.Sprintf("o%d", j%20), fmt.Sprintf("i%d", j), int64(j), int64(j)+1, "R"))
	}
	base := a.View()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id == 0 { // SelfCheck 与 View 并发只读
				if err := a.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
					return
				}
			}
			for n := 0; n < 200; n++ {
				if !reflect.DeepEqual(a.View(), base) {
					t.Error("view drifted")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
