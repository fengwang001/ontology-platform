package mono

import (
	"slices"
	"testing"

	"ontology/stack"
)

func build(shape string, n int) []int {
	a := make([]int, n)
	for i := range a {
		a[i] = 7
		if shape == "inc" {
			a[i] = i
		}
		if shape == "dec" {
			a[i] = n - i
		}
	}
	return a
}

func TestCounterLinear(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		for _, sh := range []string{"inc", "dec", "eq"} {
			sc, err := NewScanner(n)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sc.Scan(build(sh, n)); err != nil {
				t.Fatal(err)
			}
			if ops := sc.ops.Load(); ops > int64(2*n) {
				t.Errorf("shape=%s n=%d: push+pop=%d > 2n=%d", sh, n, ops, 2*n)
			}
		}
	}
}

func TestStackMonotonic(t *testing.T) {
	for _, sh := range []string{"inc", "dec", "eq"} {
		a := build(sh, 500)
		var st stack.Stack
		for i, v := range a {
			for st.Len() > 0 && a[st.Top()] < v {
				st.Pop()
			}
			if st.Len() > 0 && a[st.Top()] < v {
				t.Fatalf("%s: stack not non-increasing at i=%d", sh, i)
			}
			st.Push(i)
		}
	}
}

func TestRejections(t *testing.T) {
	sc, err := NewScanner(3)
	if err != nil {
		t.Fatal(err)
	}
	input := []int{3, 1, 2, 0}
	before := slices.Clone(input)
	cases := []struct {
		name string
		a    []int
		want error
	}{
		{"nil input", nil, ErrNilInput},
		{"too long", input, ErrTooLong},
	}
	for _, tc := range cases {
		if _, err := sc.Scan(tc.a); err != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	for _, bad := range []int{0, -7} {
		if _, err := NewScanner(bad); err != ErrBadLimit {
			t.Errorf("maxLen=%d: got %v, want ErrBadLimit", bad, err)
		}
	}
	if ErrNilInput == ErrTooLong || ErrTooLong == ErrBadLimit || ErrNilInput == ErrBadLimit {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	if !slices.Equal(input, before) {
		t.Fatal("rejected scan mutated its input")
	}
	ans, err := sc.Scan([]int{2, 1, 3})
	if err != nil || !slices.Equal(ans, []int{2, 2, None}) {
		t.Fatalf("scanner unusable after rejection: ans=%v err=%v", ans, err)
	}
}
