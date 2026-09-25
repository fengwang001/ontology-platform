package alloc

import (
	"maps"
	"math/rand"
	"strconv"
	"testing"

	"ontology/mf"
)

// naive is the independent recursive reference (rescan + recompute each step).
func naive(c int64, tasks []Task) map[string]mf.Frac {
	out := map[string]mf.Frac{}
	var live []Task
	for _, t := range tasks {
		if t.Demand == 0 {
			out[t.ID] = mf.Int(0)
		} else {
			live = append(live, t)
		}
	}
	for rem := c; len(live) > 0; {
		fair := mf.Fair(rem, len(live))
		k := 0
		for i := 1; i < len(live); i++ {
			if live[i].Demand < live[k].Demand {
				k = i
			}
		}
		if mf.Full(live[k].Demand, fair) {
			out[live[k].ID] = mf.Int(live[k].Demand)
			rem -= live[k].Demand
			live = append(live[:k], live[k+1:]...)
			continue
		}
		for _, u := range live {
			out[u.ID] = fair
		}
		break
	}
	return out
}

func eqMap(a, b map[string]mf.Frac) bool {
	return maps.EqualFunc(a, b, func(x, y mf.Frac) bool { return x.Cmp(y) == 0 })
}

func TestAllocateTable(t *testing.T) {
	tab := []struct {
		c    int64
		t    []Task
		want map[string]mf.Frac
	}{
		{30, []Task{{"A", 6}, {"B", 12}, {"C", 18}, {"D", 30}}, map[string]mf.Frac{"A": mf.Int(6), "B": mf.Int(8), "C": mf.Int(8), "D": mf.Int(8)}},
		{100, []Task{{"A", 6}, {"B", 12}, {"C", 18}, {"D", 30}}, map[string]mf.Frac{"A": mf.Int(6), "B": mf.Int(12), "C": mf.Int(18), "D": mf.Int(30)}},
		{10, []Task{{"Z", 0}, {"X", 4}}, map[string]mf.Frac{"Z": mf.Int(0), "X": mf.Int(4)}},
		{10, []Task{{"only", 30}}, map[string]mf.Frac{"only": mf.Int(10)}},
		{20, []Task{{"p", 10}, {"q", 10}, {"r", 10}}, map[string]mf.Frac{"p": mf.Make(20, 3), "q": mf.Make(20, 3), "r": mf.Make(20, 3)}},
	}
	for i, e := range tab {
		a, err := New(e.c)
		if err != nil {
			t.Fatal(err)
		}
		for _, tsk := range e.t {
			a.Add(tsk)
		}
		if !eqMap(a.Allocate(), e.want) {
			t.Fatalf("case %d mismatch", i)
		}
	}
}

func randTasks(rng *rand.Rand, n int) ([]Task, int64) {
	ts := make([]Task, n)
	var sum int64
	for i := range ts {
		ts[i] = Task{"t" + strconv.Itoa(i), int64(rng.Intn(20))} // zero demand allowed
		sum += ts[i].Demand
	}
	return ts, sum
}

func TestMatchesNaive(t *testing.T) {
	fixed := [][]Task{{{"A", 6}, {"B", 12}, {"C", 18}, {"D", 30}}, {{"Z", 0}, {"A", 6}, {"B", 12}}, {{"x", 5}, {"y", 5}, {"z", 5}}, {{"a", 1}, {"b", 1_000_000}}}
	for fi, ts := range fixed {
		for _, c := range []int64{1, 7, 30, 100, 1000} {
			a, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			for _, tsk := range ts {
				a.Add(tsk)
			}
			if !eqMap(a.Allocate(), naive(c, ts)) {
				t.Fatalf("fixed %d cap %d mismatch vs naive", fi, c)
			}
		}
	}
	rng := rand.New(rand.NewSource(42))
	for it := 0; it < 200; it++ {
		n := 1 + rng.Intn(12)
		ts, _ := randTasks(rng, n)
		c := int64(1 + rng.Intn(120))
		a, _ := New(c)
		for _, i := range rng.Perm(n) {
			a.Add(ts[i]) // random insertion order
		}
		if !eqMap(a.Allocate(), naive(c, ts)) {
			t.Fatalf("random %d mismatch", it)
		}
	}
}

func TestConservation(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for it := 0; it < 100; it++ {
		n := 1 + rng.Intn(15)
		ts, sum := randTasks(rng, n)
		c := int64(1 + rng.Intn(150))
		a, _ := New(c)
		for _, tsk := range ts {
			a.Add(tsk)
		}
		if !SumEquals(a.Allocate(), min(c, sum)) {
			t.Fatalf("%d: sum != min(C=%d,sumD=%d)", it, c, sum)
		}
	}
}

func TestExaminedCounterBounded(t *testing.T) {
	const k = 3
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		a, _ := New(int64(m * 10))
		for i := 0; i < k; i++ {
			a.Add(Task{"s" + strconv.Itoa(i), int64(i + 1)})
		}
		for i := k; i < m; i++ {
			a.Add(Task{"b" + strconv.Itoa(i), 1_000_000})
		}
		r := a.Allocate() // k full + one task revealing the level => k+1, constant in m
		if a.examined != k+1 || r["b9"].Cmp(mf.Int(1_000_000)) >= 0 {
			t.Fatalf("m=%d examined=%d level=%v", m, a.examined, r["b9"])
		}
	}
}
