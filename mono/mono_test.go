package mono

import (
	"slices"
	"testing"

	"ontology/stack"
)

var shapes = []struct {
	name string
	gen  func(n int) []int
}{
	{"increasing", func(n int) []int {
		a := make([]int, n)
		for i := range a {
			a[i] = i
		}
		return a
	}},
	{"decreasing", func(n int) []int {
		a := make([]int, n)
		for i := range a {
			a[i] = n - i
		}
		return a
	}},
	{"equal", func(n int) []int { return make([]int, n) }},
}

func TestOpsLinearBound(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		for _, sh := range shapes {
			s, err := NewScanner(n)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Scan(sh.gen(n)); err != nil {
				t.Fatal(err)
			}
			if ops := s.ops.Load(); ops > int64(2*n) {
				t.Errorf("%s n=%d: push+pop=%d > 2n=%d", sh.name, n, ops, 2*n)
			}
		}
	}
}

func TestStackMonotonic(t *testing.T) {
	cases := [][]int{{2, 2, 3}, {3, 1, 4, 1, 5, 9, 2, 6}, {5, 4, 3, 2, 1}, {1, 2, 3, 4}}
	for _, a := range cases {
		s, err := NewScanner(len(a))
		if err != nil {
			t.Fatal(err)
		}
		ans := make([]int, len(a))
		for i := range ans {
			ans[i] = None
		}
		var st stack.Stack
		for i := range a {
			s.step(&st, a, i, ans)
			var idx []int
			for st.Len() > 0 {
				top, _ := st.Pop()
				idx = append(idx, top)
			}
			for k := 1; k < len(idx); k++ { // 自顶向底值必须非递减
				if a[idx[k]] < a[idx[k-1]] {
					t.Fatalf("a=%v: stack not monotone after i=%d", a, i)
				}
			}
			for k := len(idx) - 1; k >= 0; k-- {
				st.Push(idx[k])
			}
		}
		got, _ := s.Scan(a)
		if !slices.Equal(ans, got) {
			t.Errorf("a=%v: step-driven ans=%v != Scan=%v", a, ans, got)
		}
	}
}
