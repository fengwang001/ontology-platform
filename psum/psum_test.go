package psum

import (
	"errors"
	"reflect"
	"testing"
)

type op struct {
	del    bool
	k, val int64
}

func runOps(t *testing.T, v *View, ops []op, want []int) {
	t.Helper()
	for i, o := range ops {
		var got int
		var err error
		if o.del {
			got, err = v.Del(o.k)
		} else {
			got, err = v.Put(o.k, o.val)
		}
		if err != nil {
			t.Fatalf("op %d: unexpected error %v", i, err)
		}
		if got != want[i] {
			t.Errorf("op %d: affected=%d, want %d", i, got, want[i])
		}
	}
}

// TestAffectedCounts pins invariant 2 (affected-key counts).
func TestAffectedCounts(t *testing.T) {
	cases := []struct {
		name string
		n    int
		ops  []op
		want []int
	}{
		{"notes-eight-steps", 16, []op{
			{false, 3, 5}, {false, 7, -2}, {false, 5, 4}, {false, 3, 8},
			{false, 5, 4}, {true, 3, 0}, {false, 0, 0}, {false, 7, -6},
		}, []int{1, 1, 2, 3, 0, 2, 1, 1}},
		{"zero-values", 8, []op{
			{false, 1, 0}, {false, 2, 5}, {false, 1, 0}, {true, 1, 0}, {true, 2, 0},
		}, []int{1, 1, 0, 0, 0}},
		{"suffix-ripple", 8, []op{
			{false, 1, 1}, {false, 2, 1}, {false, 3, 1}, {false, 1, 2}, {true, 2, 0},
		}, []int{1, 1, 1, 3, 1}},
	}
	for _, c := range cases {
		v, err := New(c.n, c.n)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		runOps(t, v, c.ops, c.want)
	}
}

// TestErrorPrecedence pins the four sentinels and their check order.
func TestErrorPrecedence(t *testing.T) {
	for _, args := range [][2]int{{0, 1}, {1 << 21, 1}, {1, 0}} {
		if _, err := New(args[0], args[1]); !errors.Is(err, ErrBadParam) {
			t.Errorf("New%v: want ErrBadParam", args)
		}
	}
	v, _ := New(8, 2)
	_, _ = v.Put(1, 1)
	_, _ = v.Put(2, 2)
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"bad-value", func() error { _, e := v.Put(1, 1e10); return e }, ErrBadParam},
		{"bad-value-beats-range", func() error { _, e := v.Put(9, 1e10); return e }, ErrBadParam},
		{"put-range", func() error { _, e := v.Put(9, 1); return e }, ErrOutOfRange},
		{"del-range-beats-missing", func() error { _, e := v.Del(9); return e }, ErrOutOfRange},
		{"prefix-range", func() error { _, e := v.Prefix(9); return e }, ErrOutOfRange},
		{"del-missing", func() error { _, e := v.Del(5); return e }, ErrNotFound},
		{"prefix-missing", func() error { _, e := v.Prefix(5); return e }, ErrNotFound},
		{"too-many", func() error { _, e := v.Put(3, 1); return e }, ErrTooMany},
		{"range-beats-too-many", func() error { _, e := v.Put(9, 1); return e }, ErrOutOfRange},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := v.Put(1, 5); err != nil { // modify is not capped
		t.Errorf("modify at maxKeys: %v", err)
	}
}

// TestFailureNoTrace pins invariant 4: rejected ops change nothing.
func TestFailureNoTrace(t *testing.T) {
	v, _ := New(8, 2)
	_, _ = v.Put(1, 1)
	_, _ = v.Put(2, 2)
	snap := v.View()
	rejects := []func() error{
		func() error { _, e := v.Put(1, 1e10); return e },
		func() error { _, e := v.Put(9, 1); return e },
		func() error { _, e := v.Del(9); return e },
		func() error { _, e := v.Del(5); return e },
		func() error { _, e := v.Prefix(5); return e },
		func() error { _, e := v.Put(3, 1); return e },
	}
	for i, rej := range rejects {
		if rej() == nil {
			t.Errorf("reject %d unexpectedly succeeded", i)
		}
		if !reflect.DeepEqual(v.View(), snap) {
			t.Errorf("reject %d changed the view", i)
		}
	}
	if _, err := v.Del(1); err != nil { // still usable afterwards
		t.Errorf("usable after rejects: %v", err)
	}
}

// TestVisitsLogarithmic pins the 8*(log2 N + 2) node-visit bound.
func TestVisitsLogarithmic(t *testing.T) {
	const n = 1 << 20
	bound := int64(8 * (20 + 2))
	for _, m := range []int{100, 1000, 5000, 10000} {
		v, _ := New(n, m+1)
		for i := 0; i < m; i++ {
			_, _ = v.Put(int64(i*(n/m)), 1)
		}
		ops := []struct {
			name string
			run  func() error
		}{
			{"insert", func() error { _, e := v.Put(7, 3); return e }},
			{"modify", func() error { _, e := v.Put(7, -5); return e }},
			{"delete", func() error { _, e := v.Del(7); return e }},
			{"prefix", func() error { _, e := v.Prefix(int64((m - 1) * (n / m))); return e }},
		}
		for _, o := range ops {
			if err := o.run(); err != nil {
				t.Fatalf("m=%d %s: %v", m, o.name, err)
			}
			if got := v.last.Load(); got > bound {
				t.Errorf("m=%d %s: %d node visits > bound %d", m, o.name, got, bound)
			}
		}
	}
}
