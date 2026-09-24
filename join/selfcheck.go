package join

import (
	"errors"
	"fmt"
	"maps"
	"ontology/rel"
)

type st struct {
	del     bool
	tab     string
	t       rel.Tuple
	wantLen int
}

func tp(x, y int) rel.Tuple { return rel.Tuple{X: x, Y: y} }

var eightSteps = []st{
	{false, "S", tp(1, 10), 0}, {false, "T", tp(10, 100), 0},
	{false, "R", tp(5, 1), 1}, {false, "S", tp(1, 10), 2},
	{false, "R", tp(6, 1), 4}, {true, "R", tp(5, 1), 2},
	{true, "S", tp(1, 10), 1}, {true, "S", tp(1, 10), 0},
}

func bruteForce(rs, ss, ts map[rel.Tuple]int) map[Quad]int {
	o := map[Quad]int{}
	for r, rc := range rs {
		for s, sc := range ss {
			if s.X == r.Y {
				for tt, tc := range ts {
					if tt.X == s.Y && rc*sc*tc > 0 {
						o[Quad{r.X, r.Y, s.Y, tt.Y}] += rc * sc * tc
					}
				}
			}
		}
	}
	return o
}

func totalCopies(m map[Quad]int) (n int) {
	for _, c := range m {
		n += c
	}
	return
}

// probeCheck: disjoint key sets probe 0 pairs at any m; k matches probe <= k+1.
func probeCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		w := New(0)
		for i := 0; i < m; i++ {
			_ = w.Insert("S", tp(i, i))
			_ = w.Insert("T", tp(m+i, i))
		}
		if err := w.Insert("R", tp(0, m)); err != nil { // b=m hits no S bin
			return err
		}
		if w.lastProbes != 0 {
			return fmt.Errorf("disjoint m=%d: probed %d pairs, want 0", m, w.lastProbes)
		}
	}
	const k = 5
	w := New(0)
	for i := 0; i < k; i++ {
		_ = w.Insert("S", tp(1, 2+i))
		_ = w.Insert("T", tp(2+i, 9))
	}
	if err := w.Insert("R", tp(7, 1)); err != nil {
		return err
	}
	if n := totalCopies(w.Result()); n != k || w.lastProbes > k+1 {
		return fmt.Errorf("k-match: %d copies, %d probes (want %d copies, <= %d)", n, w.lastProbes, k, k+1)
	}
	return nil
}

// SelfCheck replays built-in sequences on fresh DBs; local state only, concurrency-safe.
func (db *DB) SelfCheck() error {
	w := New(0)
	for i, op := range eightSteps {
		fn := w.Insert
		if op.del {
			fn = w.Delete
		}
		if err := fn(op.tab, op.t); err != nil {
			return fmt.Errorf("step %d: unexpected error: %w", i+1, err)
		}
		got := w.Result()
		if !maps.Equal(got, bruteForce(w.r.All(), w.s.All(), w.t.All())) {
			return fmt.Errorf("step %d: result drifts from batch recomputation", i+1)
		}
		if n := totalCopies(got); n != op.wantLen {
			return fmt.Errorf("step %d: %d copies, want %d", i+1, n, op.wantLen)
		}
		switch i {
		case 3: // duplicate S copy multiplies multiplicity (2, not a set-deduped 1)
			if got[Quad{5, 1, 10, 100}] != 2 {
				return errors.New("step 4: duplicate S copy not multiplied")
			}
		case 5: // cascade removes both copies of (5,...); (6,...) untouched
			if got[Quad{5, 1, 10, 100}] != 0 || got[Quad{6, 1, 10, 100}] != 2 {
				return errors.New("step 6: cascade delete removed the wrong combinations")
			}
		case 6: // exactly one S copy survives, detectable via a fresh matching R
			if got[Quad{6, 1, 10, 100}] != 1 {
				return errors.New("step 7: expected exactly one remaining result")
			}
			if err := w.Insert("R", tp(5, 1)); err != nil {
				return err
			}
			if totalCopies(w.Result()) != 2 || w.Result()[Quad{5, 1, 10, 100}] != 1 {
				return errors.New("step 7: S does not retain exactly one (1,10) copy")
			}
			if err := w.Delete("R", tp(5, 1)); err != nil {
				return err
			}
		}
	}
	// Three distinct decidable failures, each leaving zero trace.
	if err := w.Insert("X", rel.Tuple{}); !errors.Is(err, ErrBadTable) {
		return fmt.Errorf("bad table error: %v", err)
	}
	if err := w.Delete("R", tp(9, 9)); !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("delete-missing error: %v", err)
	}
	before := w.Result()
	_ = w.Insert("X", rel.Tuple{})
	_ = w.Delete("R", tp(9, 9))
	if !maps.Equal(before, w.Result()) {
		return errors.New("a rejected operation mutated state")
	}
	u := New(1) // first joined result fits; the second is rejected atomically
	for _, s := range []st{{false, "S", tp(1, 10), 0}, {false, "T", tp(10, 100), 0}, {false, "R", tp(5, 1), 0}} {
		if err := u.Insert(s.tab, s.t); err != nil {
			return err
		}
	}
	if err := u.Insert("R", tp(6, 1)); !errors.Is(err, ErrLimit) {
		return fmt.Errorf("limit error: %v", err)
	}
	if totalCopies(u.Result()) != 1 {
		return errors.New("rejected over-limit insert left a trace")
	}
	if err := u.Insert("R", tp(7, 9)); err != nil { // non-joining tuple still accepted
		return fmt.Errorf("database unusable after rejection: %w", err)
	}
	return probeCheck()
}
