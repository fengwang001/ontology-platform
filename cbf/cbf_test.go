package cbf

import (
	"math/rand"
	"testing"

	"ontology/hash"
)

type op struct {
	key int64
	del bool
}
type testCase struct {
	m, k           int
	maxC           uint8
	seed, keySpace int64
	nOps           int
}

var cases = []testCase{
	{7, 3, 2, 1, 8, 200},
	{31, 5, 3, 2, 20, 300},
	{101, 7, 255, 3, 50, 400},
	{397, 4, 10, 4, 100, 500},
}

func genOps(c testCase) []op {
	r := rand.New(rand.NewSource(c.seed))
	ops := make([]op, c.nOps)
	for i := range ops {
		ops[i] = op{r.Int63n(c.keySpace), r.Intn(3) == 0}
	}
	return ops
}

// replay applies random ops to a filter and a naive model in lockstep. Per
// op it asserts: rejections match the model, positions distinct, counters
// identical to the model, conservation, no false negatives, and Contains
// agreeing with model recomputation on every probe key.
func replay(t *testing.T, c testCase) {
	t.Helper()
	f, err := New(c.m, c.k, c.maxC)
	if err != nil {
		t.Fatal(err)
	}
	mc, keys, net := make([]int, c.m), map[int64]int{}, 0
	for i, o := range genOps(c) {
		pos := hash.Positions(o.key, c.m, c.k)
		seen, full := map[int]bool{}, false
		for _, p := range pos {
			if seen[p] {
				t.Fatalf("op %d: duplicate position", i)
			}
			seen[p] = true
			full = full || mc[p] == int(c.maxC)
		}
		var err error
		if o.del {
			err = f.Delete(o.key)
		} else {
			err = f.Insert(o.key)
		}
		if rejected := (o.del && keys[o.key] == 0) || (!o.del && full); rejected {
			want := ErrOverflow
			if o.del {
				want = ErrNotInserted
			}
			if err != want {
				t.Fatalf("op %d: got %v want %v", i, err, want)
			}
			continue
		}
		if err != nil {
			t.Fatalf("op %d: unexpected %v", i, err)
		}
		delta := 1
		if o.del {
			delta = -1
		}
		for _, p := range pos {
			mc[p] += delta
		}
		if keys[o.key] += delta; keys[o.key] == 0 {
			delete(keys, o.key)
		}
		net += delta
		if f.Sum() != c.k*net {
			t.Fatalf("op %d: sum=%d want %d", i, f.Sum(), c.k*net)
		}
		for p := range mc {
			if int(f.counters[p]) != mc[p] {
				t.Fatalf("op %d: counter[%d] mismatch", i, p)
			}
		}
		for key := range keys {
			if !f.Contains(key) {
				t.Fatalf("op %d: false negative for %d", i, key)
			}
		}
		for x := int64(0); x < c.keySpace; x++ {
			want := true
			for _, p := range hash.Positions(x, c.m, c.k) {
				want = want && mc[p] > 0
			}
			if f.Contains(x) != want {
				t.Fatalf("op %d: Contains(%d) disagrees with model", i, x)
			}
		}
	}
}

func runAll(t *testing.T) {
	t.Helper()
	for _, c := range cases {
		replay(t, c)
	}
}

func TestMatchesNaiveModel(t *testing.T)   { runAll(t) }
func TestNoFalseNegatives(t *testing.T)    { runAll(t) }
func TestCounterConservation(t *testing.T) { runAll(t) }

func TestAccessCountConstant(t *testing.T) {
	for _, m := range []int{101, 509, 1009, 5003, 9973} {
		f, err := New(m, 5, 255)
		if err != nil {
			t.Fatal(err)
		}
		ops := []func(){func() { f.Insert(42) }, func() { f.Contains(42) }, func() { f.Delete(42) }}
		for _, op := range ops {
			op()
			if got := f.lastAccess.Load(); got != 5 {
				t.Errorf("m=%d: accessed %d counters, want 5", m, got)
			}
		}
	}
}
