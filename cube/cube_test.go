package cube

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"ontology/dim"
)

type f3 struct {
	a, b, c string
	v       int64
}

func must(t *testing.T, n int) *Cube {
	t.Helper()
	c, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// recompute is the independent batch model: each fact adds V to its 8 cells.
func recompute(fs []f3) map[dim.Key]int64 {
	m := map[dim.Key]int64{}
	for _, f := range fs {
		for _, key := range dim.Cells(f.a, f.b, f.c) {
			if m[key] += f.v; m[key] == 0 {
				delete(m, key)
			}
		}
	}
	return m
}

// TestBatchEquivalence pins invariant 1: random same-sign streams vs batch.
func TestBatchEquivalence(t *testing.T) {
	pools := [][]f3{
		{{"a", "b", "c", 2}, {"a", "d", "c", 5}, {"e", "b", "c", 7}, {"", "b", "c", 4}, {"x", "y", "z", 11}},
		{{"a", "b", "c", -2}, {"a", "d", "c", -5}, {"e", "b", "c", -7}, {"", "b", "c", -4}, {"x", "y", "z", -11}},
	}
	for pi, pool := range pools {
		for _, seed := range []int64{1, 7, 42} {
			t.Run(strconv.Itoa(pi)+"/"+strconv.FormatInt(seed, 10), func(t *testing.T) {
				rng, cb := rand.New(rand.NewSource(seed)), must(t, 100000)
				var live []f3
				for step := 0; step < 200; step++ {
					if len(live) > 0 && rng.Intn(2) == 0 {
						i := rng.Intn(len(live))
						f := live[i]
						live = append(live[:i], live[i+1:]...)
						ok(t, cb.Remove(f.a, f.b, f.c, f.v))
					} else {
						f := pool[rng.Intn(len(pool))]
						live = append(live, f)
						ok(t, cb.Add(f.a, f.b, f.c, f.v))
					}
					want, got := recompute(live), cb.View()
					if len(got) != len(want) {
						t.Fatalf("step %d len %d!=%d", step, len(got), len(want))
					}
					for _, cell := range got {
						if want[cell.Key] != cell.Sum {
							t.Fatalf("step %d %v=%d want %d", step, cell.Key, cell.Sum, want[cell.Key])
						}
					}
				}
			})
		}
	}
}

// TestAddRemoveRoundTrip pins invariant 2: Add then Remove restores state.
func TestAddRemoveRoundTrip(t *testing.T) {
	cb := must(t, 100000)
	for _, b := range []f3{{"a", "b", "c", 2}, {"a", "d", "c", 5}, {"", "b", "", -3}} {
		ok(t, cb.Add(b.a, b.b, b.c, b.v))
	}
	for _, f := range []f3{{"e", "b", "c", 7}, {"a", "b", "c", 2}, {"", "", "", 9}, {"p", "q", "", -1}} {
		snap := cb.View()
		ok(t, cb.Add(f.a, f.b, f.c, f.v))
		ok(t, cb.Remove(f.a, f.b, f.c, f.v))
		if got := cb.View(); !reflect.DeepEqual(got, snap) {
			t.Fatalf("round trip %+v:\n%v\n%v", f, got, snap)
		}
	}
}

// TestLevelDistribution pins invariant 3: 8 keys with levels 1/3/3/1.
func TestLevelDistribution(t *testing.T) {
	vals := []string{"v", "", ","}
	for i := 0; i < 27; i++ {
		var d [4]int
		for _, key := range dim.Cells(vals[i/9], vals[(i/3)%3], vals[i%3]) {
			d[key.Level()]++
		}
		if d != [4]int{1, 3, 3, 1} {
			t.Fatalf("case %d dist=%v", i, d)
		}
	}
	if dim.All().Level() != 0 {
		t.Fatal("(*,*,*) must be level 0")
	}
}

// TestRejectionsLeaveState pins invariant 4: distinct sentinels, no trace.
func TestRejectionsLeaveState(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidMaxCells) {
		t.Fatalf("New(0) err=%v", err)
	}
	cb := must(t, 8) // one fact fills exactly 8 cells
	ok(t, cb.Add("a", "b", "c", 1))
	snap := cb.View()
	errLimit := cb.Add("p", "q", "r", 1) // 7 new cells -> 15 > 8
	errMissing := cb.Remove("p", "q", "r", 1)
	if !errors.Is(errLimit, ErrCellLimit) || !errors.Is(errMissing, ErrFactNotFound) ||
		errLimit == errMissing || errors.Is(errLimit, ErrInvalidMaxCells) {
		t.Fatalf("errors must be distinct: %v %v", errLimit, errMissing)
	}
	if !reflect.DeepEqual(cb.View(), snap) || cb.touchedCount != 0 {
		t.Fatal("rejection left a trace")
	}
	ok(t, cb.Add("a", "b", "c", -1)) // still usable; empties cube
}

// TestTouchedCountConstant pins O(8): touchedCount==8 for m=100/1000/10000.
func TestTouchedCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		cb := must(t, m*8+100)
		for i := 0; i < m; i++ {
			s := strconv.Itoa(i)
			ok(t, cb.Add(s, s, s, 1))
		}
		ok(t, cb.Add("brandnewA", "brandnewB", "brandnewC", 1))
		if cb.touchedCount != 8 {
			t.Fatalf("m=%d touched=%d, want 8", m, cb.touchedCount)
		}
	}
}
