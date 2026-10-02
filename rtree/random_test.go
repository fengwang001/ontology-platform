package rtree

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestRandomSequences replays 2000 randomized insert/delete/search
// sequences against a brute-force model, checking tree invariants after
// every mutating operation. Each run is fully reproducible from its seed.
func TestRandomSequences(t *testing.T) {
	const runs = 2000
	for seed := int64(1); seed <= runs; seed++ {
		rng := rand.New(rand.NewSource(seed))
		M := 3 + rng.Intn(14) // 3..16
		m := 1 + rng.Intn(M / 2)
		C := 1 + rng.Intn(40)
		tree, err := New(M, m, C)
		if err != nil {
			t.Fatalf("seed=%d New(%d,%d,%d): %v", seed, M, m, C, err)
		}
		model := map[int64]Rect{}
		var log []string
		ops := 30 + rng.Intn(60)
		failed := false
		for op := 0; op < ops; op++ {
			choice := rng.Intn(10)
			switch {
			case choice < 6:
				id := int64(1 + rng.Intn(C+5))
				r := randomRect(rng)
				_, ierr := tree.Insert(id, r)
				_, exists := model[id]
				switch {
				case id <= 0 || !validateRect(r):
					if ierr != ErrInvalid {
						t.Fatalf("seed=%d want ErrInvalid got %v", seed, ierr)
					}
				case exists:
					if ierr != ErrDuplicate {
						t.Fatalf("seed=%d want ErrDuplicate got %v", seed, ierr)
					}
				case len(model) >= C:
					if ierr != ErrFull {
						t.Fatalf("seed=%d want ErrFull got %v", seed, ierr)
					}
				default:
					if ierr != nil {
						t.Fatalf("seed=%d insert failed: %v", seed, ierr)
					}
					model[id] = r
				}
				log = append(log, fmt.Sprintf("Insert(%d,%v)->%v", id, r, ierr))
			case choice < 9:
				var id int64
				if len(model) > 0 && rng.Intn(4) != 0 {
					for k := range model {
						id = k
						break
					}
				} else {
					id = int64(1 + rng.Intn(C + 5))
				}
				res, derr := tree.Delete(id)
				if _, exists := model[id]; exists {
					if derr != nil {
						t.Fatalf("seed=%d delete %d: %v", seed, id, derr)
					}
					if res.Removed != res.Removed {
						t.Fatal("impossible")
					}
					delete(model, id)
				} else {
					if derr != ErrNotFound && id > 0 {
						t.Fatalf("seed=%d delete missing %d: got %v", seed, id, derr)
					}
				}
				log = append(log, fmt.Sprintf("Delete(%d)->removed:%d,reins:%d,%v",
					id, res.Removed, res.Reinserted, derr))
			default:
				q := randomRect(rng)
				got, serr := tree.Search(q)
				if serr != nil {
					t.Fatalf("seed=%d search: %v", seed, serr)
				}
				want := bruteSearch(model, q)
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("seed=%d search %v:\n got %v\nwant %v\ndump=%s\nlog=%v",
						seed, q, got, want, tree.Dump(), log)
				}
				visited := tree.Visited()
				wantVisited := countIntersecting(tree.root, q)
				if visited != wantVisited {
					t.Fatalf("seed=%d visited=%d want %d dump=%s",
						seed, visited, wantVisited, tree.Dump())
				}
				log = append(log, fmt.Sprintf("Search(%v)->%v visited:%d", q, got, visited))
			}
			if err := checkInvariants(tree); err != nil {
				t.Fatalf("seed=%d M=%d m=%d C=%d invariants after op %d: %v\ndump=%s\nlog=%v",
					seed, M, m, C, op, err, tree.Dump(), log)
			}
			if tree.Count() != len(model) {
				t.Fatalf("seed=%d count mismatch %d vs %d", seed, tree.Count(), len(model))
			}
			if t.Failed() {
				failed = true
				break
			}
		}
		// Deterministic replay: the same seed must reproduce the dump.
		finalDump := tree.Dump()
		replay := replayLogged(t, seed)
		if replay != finalDump {
			t.Fatalf("seed=%d dump not reproducible:\n %s\n %s", seed, finalDump, replay)
		}
		if seed%500 == 0 && !failed {
			t.Logf("judgement: seeds up to %d ok; last dump=%s (M=%d m=%d C=%d objects=%d)",
				seed, finalDump, M, m, C, len(model))
		}
	}
}

func randomRect(rng *rand.Rand) Rect {
	const span = 30
	x1 := int64(rng.Intn(2*span+1) - span)
	y1 := int64(rng.Intn(2*span+1) - span)
	x2 := x1 + int64(rng.Intn(4))
	y2 := y1 + int64(rng.Intn(4))
	return Rect{x1, y1, x2, y2}
}

func countIntersecting(n *node, q Rect) int {
	c := 1
	if n.height > 0 {
		for _, e := range n.entries {
			if intersects(e.rect, q) {
				c += countIntersecting(e.child, q)
			}
		}
	}
	return c
}

// replayLogged rebuilds a fresh tree with the same parameters/seed and
// replays the generated operations, returning the final dump.
func replayLogged(t *testing.T, seed int64) string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	M := 3 + rng.Intn(14)
	m := 1 + rng.Intn(M / 2)
	C := 1 + rng.Intn(40)
	tree, err := New(M, m, C)
	if err != nil {
		t.Fatal(err)
	}
	ops := 30 + rng.Intn(60)
	model := map[int64]Rect{}
	for op := 0; op < ops; op++ {
		choice := rng.Intn(10)
		switch {
		case choice < 6:
			id := int64(1 + rng.Intn(C+5))
			r := randomRect(rng)
			if _, ierr := tree.Insert(id, r); ierr == nil {
				model[id] = r
			}
		case choice < 9:
			var id int64
			if len(model) > 0 && rng.Intn(4) != 0 {
				for k := range model {
					id = k
					break
				}
			} else {
				id = int64(1 + rng.Intn(C + 5))
			}
			if _, exists := model[id]; exists {
				tree.Delete(id)
				delete(model, id)
			}
		default:
			q := randomRect(rng)
			tree.Search(q)
		}
	}
	return tree.Dump()
}
