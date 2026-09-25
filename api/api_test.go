package api

import (
	"errors"
	"sync"
	"testing"

	"ontology/dep"
)

func sevenCols() []dep.Column {
	sum := func(v []int) int { return v[0] + v[1] }
	return []dep.Column{
		{Name: "a", Base: true, Initial: 2}, {Name: "b", Base: true, Initial: 3},
		{Name: "c", Base: true, Initial: 4},
		{Name: "d", Deps: []string{"a", "b"}, Fn: sum},
		{Name: "e", Deps: []string{"d"}, Fn: func(v []int) int { return v[0] * 2 }},
		{Name: "f", Deps: []string{"c", "e"}, Fn: sum},
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err=%v want %v", err, target)
	}
}

// 构建失败不影响后续正常构建，运行期被拒操作不改状态、实例仍可用。
func TestErrors(t *testing.T) {
	id := func(v []int) int { return v[0] }
	b0 := dep.Column{Name: "a", Base: true}
	mk := func(n string, ds ...string) dep.Column { return dep.Column{Name: n, Deps: ds, Fn: id} }
	builds := []struct {
		cs   []dep.Column
		want error
	}{
		{[]dep.Column{b0, b0}, dep.ErrDuplicateColumn},
		{[]dep.Column{b0, mk("x", "zzz")}, dep.ErrUndeclaredDep},
		{[]dep.Column{b0, mk("x", "y"), mk("y", "x")}, dep.ErrCyclic},
		{[]dep.Column{b0, mk("x", "x")}, dep.ErrCyclic},
	}
	for _, c := range builds {
		_, err := New(dep.Spec{Columns: c.cs})
		wantErr(t, err, c.want)
	}
	sentinels := []error{dep.ErrCyclic, dep.ErrUndeclaredDep, ErrUnknown, ErrSetDerived}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
	tb, _ := New(dep.Spec{Columns: sevenCols()})
	_, e := tb.Get("zzz")
	wantErr(t, e, ErrUnknown)
	wantErr(t, tb.Set("zzz", 1), ErrUnknown)
	wantErr(t, tb.Set("d", 1), ErrSetDerived)
	if v, _ := tb.Get("f"); v != 14 { // 被拒后状态不变、仍可正常使用
		t.Fatalf("after rejects Get(f)=%d want 14", v)
	}
}

func TestSevenSteps(t *testing.T) {
	tb, _ := New(dep.Spec{Columns: sevenCols()})
	cols := []string{"e", "d", "f", "d", "f", "e"}
	vals := []int{10, 5, 14, 8, 20, 16}
	for i := range cols {
		if i == 3 {
			if err := tb.Set("a", 5); err != nil {
				t.Fatal(err)
			}
		}
		if v, e := tb.Get(cols[i]); e != nil || v != vals[i] {
			t.Fatalf("step %d Get(%s)=%d,%v want %d", i+1, cols[i], v, e, vals[i])
		}
	}
	if err := tb.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestNaiveAgreement(t *testing.T) {
	tb, _ := New(dep.Spec{Columns: sevenCols()})
	bm := map[string]int{"a": 2, "b": 3, "c": 4}
	var nv func(string) int
	nv = func(n string) int {
		switch n {
		case "d":
			return nv("a") + nv("b")
		case "e":
			return nv("d") * 2
		case "f":
			return nv("c") + nv("e")
		}
		return bm[n]
	}
	ops := []struct {
		set bool
		col string
		val int
	}{
		{false, "f", 0}, {true, "b", 10}, {false, "e", 0},
		{true, "c", 1}, {false, "f", 0}, {true, "a", 7}, {false, "d", 0},
	}
	all := []string{"a", "b", "c", "d", "e", "f"}
	for i, o := range ops {
		if o.set {
			if err := tb.Set(o.col, o.val); err != nil {
				t.Fatal(err)
			}
			bm[o.col] = o.val
		} else if v, _ := tb.Get(o.col); v != nv(o.col) {
			t.Fatalf("step %d Get(%s)=%d want %d", i, o.col, v, nv(o.col))
		}
		for _, n := range all {
			if v, _ := tb.Get(n); v != nv(n) {
				t.Fatalf("step %d %s=%d want naive %d", i, n, v, nv(n))
			}
		}
	}
}

func TestConcurrentSelfCheck(t *testing.T) {
	tb, _ := New(dep.Spec{Columns: sevenCols()})
	for _, n := range []string{"a", "d", "e", "f"} {
		tb.Get(n)
	}
	const N = 16
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for g := 0; g < N; g++ {
		go func() {
			defer wg.Done()
			if err := tb.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			for r := 0; r < 100; r++ {
				if v, e := tb.Get("f"); e != nil || v != 14 {
					t.Errorf("Get(f)=%d,%v", v, e)
				}
			}
		}()
	}
	wg.Wait()
}
