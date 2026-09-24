package api

import (
	"errors"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestErrorsDistinct(t *testing.T) {
	f, err := New(7, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Insert(3)
	_ = f.Insert(3)
	for _, tc := range []struct {
		name string
		got  error
		want error
	}{
		{"overflow", f.Insert(3), ErrOverflow},
		{"not-inserted", f.Delete(2), ErrNotInserted},
	} {
		if !errors.Is(tc.got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if _, err := New(6, 3, 2); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("bad params: %v", err)
	}
	for sentinel, other := range map[error][]error{
		ErrOverflow:      {ErrNotInserted, ErrInvalidParams},
		ErrNotInserted:   {ErrOverflow, ErrInvalidParams},
		ErrInvalidParams: {ErrOverflow, ErrNotInserted},
	} {
		for _, o := range other {
			if errors.Is(sentinel, o) {
				t.Errorf("%v must differ from %v", sentinel, o)
			}
		}
	}
}

func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	for _, p := range [][3]int{{6, 3, 2}, {7, 7, 2}, {7, 0, 2}, {7, 3, 0}, {4, 2, 5}} {
		if _, err := New(p[0], p[1], uint8(p[2])); !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("New%v: %v", p, err)
		}
	}
	f, _ := New(7, 3, 2)
	_ = f.Insert(3)
	_ = f.Insert(3)
	before := f.Snapshot()
	if err := f.Insert(3); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow: %v", err)
	}
	if err := f.Delete(2); !errors.Is(err, ErrNotInserted) {
		t.Fatalf("not-inserted: %v", err)
	}
	if err := f.Delete(2); !errors.Is(err, ErrNotInserted) {
		t.Fatal("rejected delete left a phantom key behind")
	}
	after := f.Snapshot()
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("counter[%d] mutated by rejected op", i)
		}
	}
	if err := f.Insert(2); err != nil || !f.Contains(3) {
		t.Fatal("filter unusable after rejections")
	}
}

func TestConcurrentContainsConsistent(t *testing.T) {
	f, err := New(509, 7, 255)
	if err != nil {
		t.Fatal(err)
	}
	for x := int64(0); x < 200; x++ {
		if f.Insert(x) != nil {
			t.Fatal("unexpected overflow")
		}
	}
	want := make([]bool, 400)
	for i := range want {
		want[i] = f.Contains(int64(i))
	}
	const n = 16
	got := make([][]bool, n)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := make([]bool, 400)
			for rep := 0; rep < 50; rep++ {
				for i := range r {
					r[i] = f.Contains(int64(i))
				}
			}
			got[g] = r
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		for i := range want {
			if got[g][i] != want[i] {
				t.Fatalf("goroutine %d key %d: %v want %v", g, i, got[g][i], want[i])
			}
		}
	}
}
