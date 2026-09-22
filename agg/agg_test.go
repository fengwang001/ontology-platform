package agg

import (
	"math"
	"testing"
)

func TestWithdrawalDeclarations(t *testing.T) {
	want := map[Kind]bool{
		Count: false, Sum: false, Min: true, Max: true, DistinctCount: true,
	}
	for k, need := range want {
		if New(k).NeedsMembersOnDelete() != need {
			t.Fatalf("%s NeedsMembersOnDelete = %v, want %v", k, !need, need)
		}
	}
}

func TestCountSumIncremental(t *testing.T) {
	c := New(Count).(*countAgg)
	s := newSumAgg()
	mems := []Member{{"a", 1}, {"b", 2.5}, {"c", -3}}
	for _, m := range mems {
		c.Insert(m)
		s.Insert(m)
	}
	if v, _ := c.Value(); v != 3 {
		t.Fatalf("count=%v", v)
	}
	if v, _ := s.Value(); v != 0.5 {
		t.Fatalf("sum=%v", v)
	}
	if ok := c.DeleteIncremental(mems[0]); !ok {
		t.Fatal("count delete must be incremental")
	}
	if ok := s.DeleteIncremental(mems[0]); !ok {
		t.Fatal("sum delete must be incremental")
	}
	if v, _ := c.Value(); v != 2 {
		t.Fatalf("count after delete=%v", v)
	}
	if v, _ := s.Value(); v != -0.5 {
		t.Fatalf("sum after delete=%v", v)
	}
}

func TestMinMaxExtremumDeletion(t *testing.T) {
	t.Run("min", func(t *testing.T) {
		a := New(Min)
		for _, m := range []Member{{"a", 5}, {"b", 1}, {"c", 9}, {"d", 1}} {
			a.Insert(m)
		}
		if !a.DeleteIncremental(Member{"c", 9}) {
			t.Fatal("deleting non-extremum 9 must be incremental")
		}
		if !a.DeleteIncremental(Member{"b", 1}) {
			t.Fatal("deleting one of two tied minima must stay incremental")
		}
		if a.DeleteIncremental(Member{"d", 1}) {
			t.Fatal("deleting the unique remaining minimum demands recompute")
		}
	})
	t.Run("max", func(t *testing.T) {
		a := New(Max)
		for _, m := range []Member{{"a", 5}, {"b", 1}, {"c", 9}, {"d", 3}} {
			a.Insert(m)
		}
		if !a.DeleteIncremental(Member{"b", 1}) {
			t.Fatal("deleting non-extremum 1 must be incremental")
		}
		if a.DeleteIncremental(Member{"c", 9}) {
			t.Fatal("deleting the unique maximum demands recompute")
		}
	})
}

func TestDistinctAlwaysNeedsMembers(t *testing.T) {
	a := New(DistinctCount)
	a.Insert(Member{"a", 1})
	a.Insert(Member{"b", 1})
	a.Insert(Member{"c", 2})
	if v, _ := a.Value(); v != 2 {
		t.Fatalf("distinct=%v", v)
	}
	// With the occurrence-table fast path, removing a value still held by
	// another member stays incremental; removing the last holder of a
	// value is incremental too but is the case a scalar distinct count
	// could not answer (documented family requirement == true).
	if !a.DeleteIncremental(Member{"a", 1}) {
		t.Fatal("distinct delete with occurrence table must succeed")
	}
	if v, _ := a.Value(); v != 2 {
		t.Fatalf("distinct after first delete=%v", v)
	}
	if !a.DeleteIncremental(Member{"b", 1}) {
		t.Fatal("distinct delete last holder of value")
	}
	if v, _ := a.Value(); v != 1 {
		t.Fatalf("distinct after second delete=%v", v)
	}
}

func TestEmptyAggregateHasNoValue(t *testing.T) {
	for _, k := range Family {
		a := New(k)
		if v, ok := a.Value(); ok {
			t.Fatalf("%s empty reports value %v", k, v)
		}
	}
}

func TestSumBitStableAcrossOrders(t *testing.T) {
	values := []Member{{"a", 0.1}, {"b", 0.2}, {"c", 0.3}, {"d", 1e16}}
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}}
	var bits []uint64
	for _, ord := range orders {
		s := newSumAgg()
		for _, i := range ord {
			s.Insert(values[i])
		}
		v, _ := s.Value()
		bits = append(bits, math.Float64bits(v))
	}
	for i := 1; i < len(bits); i++ {
		if bits[i] != bits[0] {
			t.Fatalf("sum bits differ by order: %x %x", bits[0], bits[i])
		}
	}
}
