package api_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/api"
)

func TestInvalidParamsTable(t *testing.T) {
	cases := []struct{ nb, epb, kicks int }{
		{0, 1, 1}, {1, 1, 1}, {3, 1, 1}, {6, 1, 1}, {4, 0, 1}, {4, -1, 1}, {4, 1, 0}, {4, 1, -2},
	}
	for _, c := range cases {
		if _, err := api.New(c.nb, c.epb, c.kicks); !errors.Is(err, api.ErrInvalidParams) {
			t.Fatalf("New(%d,%d,%d): want ErrInvalidParams, got %v", c.nb, c.epb, c.kicks, err)
		}
	}
	for _, c := range []struct{ nb, epb, kicks int }{{2, 1, 1}, {4, 2, 4}, {1024, 4, 8}} {
		if _, err := api.New(c.nb, c.epb, c.kicks); err != nil {
			t.Fatalf("New(%d,%d,%d): %v", c.nb, c.epb, c.kicks, err)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	errs := []error{api.ErrInvalidParams, api.ErrNegativeKey, api.ErrFull, api.ErrNotInserted}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("errors %d and %d not distinct", i, j)
			}
		}
	}
}

// 第三节十步序列：逐步核对桶状态与第 7、8、9 步判定。
func TestTenStepTable(t *testing.T) {
	f, err := api.New(4, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		op   func() bool
		want [][]uint8
	}
	steps := []step{
		{func() bool { return f.Insert(1) == nil }, [][]uint8{{}, {2}, {}, {}}},
		{func() bool { return f.Insert(5) == nil }, [][]uint8{{}, {2, 6}, {}, {}}},
		{func() bool { return f.Insert(9) == nil }, [][]uint8{{3}, {2, 6}, {}, {}}},
		{func() bool { return f.Insert(2) == nil }, [][]uint8{{3}, {2, 6}, {3}, {}}},
		{func() bool { return f.Insert(6) == nil }, [][]uint8{{3}, {2, 6}, {3, 7}, {}}},
		{func() bool { return f.Insert(10) == nil }, [][]uint8{{3, 4}, {2, 6}, {3, 7}, {}}},
		{func() bool { return f.Insert(14) == nil }, [][]uint8{{3, 4}, {2, 6}, {1, 7}, {3}}},
		{func() bool { return f.Lookup(2) }, [][]uint8{{3, 4}, {2, 6}, {1, 7}, {3}}},
		{func() bool { return f.Delete(9) == nil }, [][]uint8{{4}, {2, 6}, {1, 7}, {3}}},
		{func() bool { return f.Lookup(2) }, [][]uint8{{4}, {2, 6}, {1, 7}, {3}}},
	}
	for i, s := range steps {
		if !s.op() {
			t.Fatalf("step %d op failed", i+1)
		}
		if got := norm(f.Buckets()); !reflect.DeepEqual(got, norm(s.want)) {
			t.Fatalf("step %d: buckets=%v, want %v", i+1, got, s.want)
		}
	}
}

// (丙)：Delete(23) 未插入，正确实现返回 ErrNotInserted，x=2 的指纹不受影响。
func TestDeleteNonInsertedCollision(t *testing.T) {
	f, _ := api.New(4, 2, 4)
	for _, x := range []int64{1, 5, 9, 2, 6, 10, 14} {
		if err := f.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Delete(9); err != nil {
		t.Fatal(err)
	}
	before := f.Buckets()
	if err := f.Delete(23); !errors.Is(err, api.ErrNotInserted) {
		t.Fatalf("want ErrNotInserted, got %v", err)
	}
	if !reflect.DeepEqual(f.Buckets(), before) {
		t.Fatal("Delete(23) changed state")
	}
	if !f.Lookup(2) {
		t.Fatal("Lookup(2) false after rejected Delete(23)")
	}
}

func TestNegativeKey(t *testing.T) {
	f, _ := api.New(4, 2, 4)
	if err := f.Insert(-1); !errors.Is(err, api.ErrNegativeKey) {
		t.Fatalf("want ErrNegativeKey, got %v", err)
	}
	if err := f.Delete(-1); !errors.Is(err, api.ErrNegativeKey) {
		t.Fatalf("want ErrNegativeKey, got %v", err)
	}
	if f.Lookup(-1) {
		t.Fatal("negative key lookup must be false")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func norm(b [][]uint8) [][]uint8 {
	out := make([][]uint8, len(b))
	for i, v := range b {
		if v == nil {
			out[i] = []uint8{}
		} else {
			out[i] = v
		}
	}
	return out
}
