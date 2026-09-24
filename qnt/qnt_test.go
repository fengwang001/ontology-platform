package qnt

import (
	"math/rand"
	"sort"
	"testing"
)

type op struct {
	ins bool
	v   int64
}

func genOps(seed int64, steps, span int) []op {
	r := rand.New(rand.NewSource(seed))
	var live []int64
	out := make([]op, 0, steps)
	for i := 0; i < steps; i++ {
		if len(live) == 0 || r.Intn(3) > 0 {
			v := int64(r.Intn(span) - span/2)
			out, live = append(out, op{true, v}), append(live, v)
		} else {
			j := r.Intn(len(live))
			out, live = append(out, op{false, live[j]}), append(live[:j], live[j+1:]...)
		}
	}
	return out
}
func naiveMedian(s []int64) float64 { return (float64(s[(len(s)-1)/2]) + float64(s[len(s)/2])) / 2 }
func naiveP90(s []int64) int64      { return s[(90*len(s)+99)/100-1] }
func apply(t *testing.T, e *Engine, ms []int64, o op) []int64 {
	t.Helper()
	if o.ins {
		e.Insert(o.v)
		return append(ms, o.v)
	}
	if err := e.Delete(o.v); err != nil {
		t.Fatal(err)
	}
	for i, x := range ms {
		if x == o.v {
			return append(ms[:i], ms[i+1:]...)
		}
	}
	t.Fatal("mirror lacked the deleted value")
	return ms
}
func assertMatch(t *testing.T, e *Engine, ms []int64) {
	t.Helper()
	if e.Count() != len(ms) {
		t.Fatalf("count %d != %d", e.Count(), len(ms))
	}
	if len(ms) == 0 {
		if _, err := e.Median(); err != ErrEmpty {
			t.Fatal("empty Median must be ErrEmpty")
		} else if _, err := e.QuantileP90(); err != ErrEmpty {
			t.Fatal("empty P90 must be ErrEmpty")
		}
		return
	}
	for k := 1; k <= len(ms); k++ {
		if g, err := e.Kth(k); err != nil || g != ms[k-1] {
			t.Fatalf("Kth(%d)=%d,%v want %d", k, g, err, ms[k-1])
		}
	}
	m, err := e.Median()
	p, perr := e.QuantileP90()
	if err != nil || perr != nil || m != naiveMedian(ms) || p != naiveP90(ms) || m > float64(p) {
		t.Fatalf("quantiles differ from batch mirror: m=%v p=%d ms=%v", m, p, ms)
	}
}
func sorted(ms []int64) []int64 {
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	return ms
}
func TestNaiveBatchConsistency(t *testing.T) {
	cases := []struct {
		name        string
		seed        int64
		steps, span int
	}{
		{"tiny", 1, 200, 4}, {"dups", 2, 2000, 8}, {"wide", 3, 2000, 100000},
		{"equal", 4, 500, 1}, {"neg", 5, 1500, 2000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, ms := New(), []int64{}
			for _, o := range genOps(c.seed, c.steps, c.span) {
				ms = sorted(apply(t, e, ms, o))
				assertMatch(t, e, ms)
			}
		})
	}
}
func TestMedianNotAboveP90(t *testing.T) {
	for _, seed := range []int64{11, 22, 33} {
		e, ms := New(), []int64{}
		for _, o := range genOps(seed, 1000, 50) {
			ms = sorted(apply(t, e, ms, o))
			assertMatch(t, e, ms)
		}
	}
}
func TestKthSequenceAndCount(t *testing.T) {
	e, ms := New(), []int64{}
	for i, o := range genOps(99, 3000, 500) {
		ms = apply(t, e, ms, o)
		if i%50 == 0 {
			assertMatch(t, e, sorted(ms))
		}
	}
}
func TestDescentVisitsLogarithmic(t *testing.T) {
	for _, m := range []int{100, 333, 1000, 4097, 10000} {
		e := New()
		for _, i := range rand.New(rand.NewSource(int64(m))).Perm(m) {
			e.Insert(int64(i))
		}
		if _, err := e.Median(); err != nil {
			t.Fatal(err)
		}
		bound := 2*ceilLog2(m) + 2
		if v := int(e.visits.Load()); v > bound || v >= 64 {
			t.Fatalf("m=%d visits=%d bound=%d", m, v, bound)
		}
	}
}
func TestKthAndAbsentErrors(t *testing.T) {
	e := New()
	if _, err := e.Kth(1); err == nil {
		t.Fatal("Kth on empty must error")
	}
	e.Insert(5)
	for _, k := range []int{0, -1, 2, 100} {
		if _, err := e.Kth(k); err == nil {
			t.Fatalf("Kth(%d) must error", k)
		}
	}
	if err := e.Delete(6); err != ErrAbsent {
		t.Fatalf("Delete absent err=%v", err)
	}
}
func TestMedianInt64Extremes(t *testing.T) {
	e := New()
	e.Insert(1<<63 - 1)
	e.Insert(-1 << 63)
	if got, err := e.Median(); err != nil || got != (float64(int64(1<<63-1))+float64(int64(-1<<63)))/2 {
		t.Fatalf("extremes median=%v,%v", got, err)
	}
}
