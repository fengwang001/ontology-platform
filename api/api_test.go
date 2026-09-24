package api

import (
	"errors"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestEightStepTrace(t *testing.T) {
	d, want := New(), []string{"a", "ab", "abx", "abcx", "abcxy", "bcxy", "zbcxy", "zcxy"}
	for i, o := range eightOps() {
		if err := apply(d, o); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if d.Text() != want[i] {
			t.Fatalf("step %d: %q, want %q", i+1, d.Text(), want[i])
		}
	}
}

// TestConvergence pins invariant 2 across different delivery orders.
func TestConvergence(t *testing.T) {
	orders := [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7},
		{0, 2, 1, 4, 3, 5, 6, 7},
		{0, 2, 1, 3, 4, 6, 5, 7},
		{0, 2, 1, 4, 3, 6, 5, 7},
	}
	ops := eightOps()
	for n, ord := range orders {
		d := New()
		for _, i := range ord {
			if err := apply(d, ops[i]); err != nil {
				t.Fatalf("order %d op %d: %v", n, i, err)
			}
		}
		if d.Text() != "zcxy" {
			t.Fatalf("order %d: %q, want zcxy", n, d.Text())
		}
	}
}

// TestTombstone pins invariant 3: deleted nodes stay anchors and keep
// descendants; repeated/missing deletes are decidable errors.
func TestTombstone(t *testing.T) {
	d := New()
	type step struct {
		fn   func() error
		want string
	}
	for _, s := range []step{
		{func() error { return d.Insert(ID{}, eid(1, "A"), 'a') }, "a"},
		{func() error { return d.Insert(eid(1, "A"), eid(2, "A"), 'b') }, "ab"},
		{func() error { return d.Insert(eid(1, "A"), eid(1, "B"), 'x') }, "abx"},
		{func() error { return d.Insert(eid(2, "A"), eid(3, "A"), 'c') }, "abcx"},
		{func() error { return d.Delete(eid(1, "A")) }, "bcx"},
		{func() error { return d.Insert(eid(1, "A"), eid(3, "B"), 'z') }, "zbcx"},
		{func() error { return d.Delete(eid(2, "A")) }, "zcx"},
	} {
		if err := s.fn(); err != nil || d.Text() != s.want {
			t.Fatalf("got %q err=%v want %q", d.Text(), err, s.want)
		}
	}
	if err := d.Delete(eid(2, "A")); !errors.Is(err, ErrAlreadyDeleted) {
		t.Fatalf("repeat delete err=%v, want ErrAlreadyDeleted", err)
	}
	if err := d.Delete(eid(9, "Q")); !errors.Is(err, ErrDeleteNotFound) {
		t.Fatalf("missing delete err=%v, want ErrDeleteNotFound", err)
	}
	if d.Text() != "zcx" {
		t.Fatalf("failed deletes changed state: %q", d.Text())
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4: three distinct decidable
// failures leave no trace and the doc stays usable.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		call func(*Doc) error
		want error
	}{
		{"duplicate id", func(d *Doc) error { return d.Insert(ID{}, eid(1, "A"), 'q') }, ErrDuplicateID},
		{"missing prev", func(d *Doc) error { return d.Insert(eid(9, "A"), eid(2, "A"), 'q') }, ErrPrevNotFound},
		{"invalid id", func(d *Doc) error { return d.Insert(ID{}, ID{Lamport: 3}, 'q') }, ErrInvalidID},
	}
	if errors.Is(ErrDuplicateID, ErrPrevNotFound) || errors.Is(ErrDuplicateID, ErrInvalidID) || errors.Is(ErrPrevNotFound, ErrInvalidID) {
		t.Fatal("the three failure errors must be distinct")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			if err := d.Insert(ID{}, eid(1, "A"), 'a'); err != nil {
				t.Fatal(err)
			}
			if err := tc.call(d); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
			if d.Text() != "a" {
				t.Fatalf("rejected op left state: %q", d.Text())
			}
			if err := d.Insert(eid(1, "A"), eid(2, "A"), 'b'); err != nil || d.Text() != "ab" {
				t.Fatalf("doc unusable after reject: %q err=%v", d.Text(), err)
			}
		})
	}
}

// TestConcurrentReaders: N goroutines read a fed instance concurrently;
// every Text result is identical. No sleeps.
func TestConcurrentReaders(t *testing.T) {
	d := New()
	for _, o := range eightOps() {
		if err := apply(d, o); err != nil {
			t.Fatal(err)
		}
	}
	const N = 16
	res, start := make([]string, N), make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for k := 0; k < 200; k++ {
				if err := d.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
					return
				}
				res[i] = d.Text()
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if res[i] != res[0] {
			t.Fatalf("reader %d got %q, want %q", i, res[i], res[0])
		}
	}
	if res[0] != "zcxy" {
		t.Fatalf("concurrent text %q, want zcxy", res[0])
	}
}
