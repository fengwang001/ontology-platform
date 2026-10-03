package maglev

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveTable is a deliberately plain, step-by-step re-implementation of the
// spec used as an oracle: it recomputes T* from scratch and replays the
// mandatory/optional migration rules slot by slot.
type naiveTable struct {
	m, lim   int
	backends map[string][3]int // name -> {offset, skip, weight}
	cur      []string
}

func newNaive(m, lim int) *naiveTable {
	return &naiveTable{m: m, lim: lim, backends: make(map[string][3]int), cur: make([]string, m)}
}

// target builds T* exactly as specified: backends sorted by name, each with
// a pointer j into perm(j) = (offset + j*skip) mod m, rounds of weight
// takes each, stopping the instant the table is full.
func (n *naiveTable) target() []string {
	t := make([]string, n.m)
	if len(n.backends) == 0 {
		return t
	}
	names := make([]string, 0, len(n.backends))
	for name := range n.backends {
		names = append(names, name)
	}
	sort.Strings(names)
	js := make([]int, len(names))
	filled := 0
	for filled < n.m {
		for i, name := range names {
			b := n.backends[name]
			for w := 0; w < b[2]; w++ {
				for {
					s := (b[0] + js[i]*b[1]) % n.m
					js[i]++
					if t[s] == "" {
						t[s] = name
						filled++
						break
					}
				}
				if filled == n.m {
					return t
				}
			}
		}
	}
	return t
}

func (n *naiveTable) migrate() int {
	tstar := n.target()
	changed := 0
	for s := 0; s < n.m; s++ {
		owner := n.cur[s]
		_, alive := n.backends[owner]
		if owner == "" || !alive {
			if n.cur[s] != tstar[s] {
				changed++
			}
			n.cur[s] = tstar[s]
		}
	}
	left := n.lim
	for s := 0; s < n.m && left > 0; s++ {
		if n.cur[s] != tstar[s] {
			n.cur[s] = tstar[s]
			changed++
			left--
		}
	}
	return changed
}

func (n *naiveTable) add(name string, offset, skip, weight int) (int, error) {
	if name == "" {
		return 0, ErrEmptyName
	}
	if offset < 0 || offset >= n.m || skip < 1 || skip >= n.m || weight < 1 || weight > 16 {
		return 0, ErrOutOfRange
	}
	if _, ok := n.backends[name]; ok {
		return 0, ErrNameExists
	}
	if len(n.backends) >= n.m {
		return 0, ErrTooManyBackends
	}
	n.backends[name] = [3]int{offset, skip, weight}
	return n.migrate(), nil
}

func (n *naiveTable) remove(name string) (int, error) {
	if _, ok := n.backends[name]; !ok {
		return 0, ErrBackendNotFound
	}
	delete(n.backends, name)
	return n.migrate(), nil
}

func (n *naiveTable) step() int {
	if len(n.backends) == 0 {
		return 0
	}
	tstar := n.target()
	changed := 0
	left := n.lim
	for s := 0; s < n.m && left > 0; s++ {
		if n.cur[s] != tstar[s] {
			n.cur[s] = tstar[s]
			changed++
			left--
		}
	}
	return changed
}

func (n *naiveTable) pending() int {
	tstar := n.target()
	c := 0
	for s := 0; s < n.m; s++ {
		if n.cur[s] != tstar[s] {
			c++
		}
	}
	return c
}

var smallPrimes = []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37}

// TestRandomAgainstNaive replays 2000 random backend sets and random
// operation sequences against the naive oracle, comparing every return
// value, Pending, Table and Lookup, plus the structural invariants. Inputs,
// outputs and the verdict basis are logged per group.
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const groups = 2000
	for g := 0; g < groups; g++ {
		m := smallPrimes[rng.Intn(len(smallPrimes))]
		lim := 1 + rng.Intn(m)
		tb, err := New(m, lim)
		if err != nil {
			t.Fatalf("group %d: New(%d, %d) failed: %v", g, m, lim, err)
		}
		oracle := newNaive(m, lim)
		names := []string{"alpha", "beta", "gamma", "delta", "eps", "zeta", "eta", "theta"}
		ops := 20 + rng.Intn(30)
		log := fmt.Sprintf("group %d: M=%d Lim=%d ops=%d", g, m, lim, ops)

		for op := 0; op < ops; op++ {
			var gotN, wantN int
			var gotErr, wantErr error
			desc := ""
			switch rng.Intn(5) {
			case 0, 1: // add, sometimes with invalid parameters
				name := names[rng.Intn(len(names))]
				if rng.Intn(20) == 0 {
					name = ""
				}
				offset, skip, weight := rng.Intn(m), 1+rng.Intn(m-1), 1+rng.Intn(16)
				if rng.Intn(20) == 0 { // inject an out-of-range parameter
					switch rng.Intn(3) {
					case 0:
						offset = m + rng.Intn(3)
					case 1:
						skip = 0
					case 2:
						weight = 17 + rng.Intn(3)
					}
				}
				gotN, gotErr = tb.AddBackend(name, offset, skip, weight)
				wantN, wantErr = oracle.add(name, offset, skip, weight)
				desc = fmt.Sprintf("Add(%q,%d,%d,%d)", name, offset, skip, weight)
			case 2: // remove
				name := names[rng.Intn(len(names))]
				gotN, gotErr = tb.RemoveBackend(name)
				wantN, wantErr = oracle.remove(name)
				desc = fmt.Sprintf("Remove(%q)", name)
			case 3: // step
				gotN, wantN = tb.Step(), oracle.step()
				desc = "Step()"
			case 4: // lookup
				h := rng.Uint64()
				gotName, gotErr := tb.Lookup(h)
				wantName := ""
				if len(oracle.backends) > 0 {
					wantName = oracle.cur[h%uint64(m)]
				}
				if len(oracle.backends) == 0 {
					if !errors.Is(gotErr, ErrNoBackends) {
						t.Fatalf("%s\nop %d: Lookup(%d) err = %v, want ErrNoBackends", log, op, h, gotErr)
					}
				} else if gotErr != nil || gotName != wantName {
					t.Fatalf("%s\nop %d: Lookup(%d) = %q, %v; want %q", log, op, h, gotName, gotErr, wantName)
				}
				continue
			}
			if gotN != wantN || !errors.Is(gotErr, wantErr) {
				t.Fatalf("%s\nop %d: %s = (%d, %v), oracle = (%d, %v)",
					log, op, desc, gotN, gotErr, wantN, wantErr)
			}
			if got, want := tb.Pending(), oracle.pending(); got != want {
				t.Fatalf("%s\nop %d: %s\nPending = %d, oracle = %d", log, op, desc, got, want)
			}
			gotTab, wantTab := tb.Table(), oracle.cur
			for s := 0; s < m; s++ {
				if gotTab[s] != wantTab[s] {
					t.Fatalf("%s\nop %d: %s\ntable[%d] = %q, oracle = %q\ngot  %v\nwant %v",
						log, op, desc, s, gotTab[s], wantTab[s], gotTab, wantTab)
				}
			}
			// Invariants: a non-empty set implies full ownership by live
			// backends; Pending 0 implies cur == T*.
			if len(oracle.backends) > 0 {
				for s := 0; s < m; s++ {
					if _, ok := oracle.backends[gotTab[s]]; !ok {
						t.Fatalf("%s\nop %d: %s\nslot %d owned by %q, not in backend set",
							log, op, desc, s, gotTab[s])
					}
				}
			}
			if tb.Pending() == 0 {
				tstar := oracle.target()
				for s := 0; s < m; s++ {
					if gotTab[s] != tstar[s] {
						t.Fatalf("%s\nop %d: %s\nPending=0 but table[%d]=%q != T*[%d]=%q",
							log, op, desc, s, gotTab[s], s, tstar[s])
					}
				}
			}
		}
		t.Logf("%s -> OK: all %d ops matched oracle (return values, Pending, Table, Lookup, invariants)",
			log, ops)
	}
}

// TestMidMigrationRepartition: registering or removing while a migration is
// still in flight re-partitions slots into mandatory/optional against the
// new T* and the old cur.
func TestMidMigrationRepartition(t *testing.T) {
	tb := mustNew(t, 7, 1)
	oracle := newNaive(7, 1)

	type op struct {
		desc string
		run  func() (int, error)
		want func() (int, error)
	}
	ops := []op{
		{"Add A", func() (int, error) { return tb.AddBackend("A", 3, 4, 1) }, func() (int, error) { return oracle.add("A", 3, 4, 1) }},
		{"Add B", func() (int, error) { return tb.AddBackend("B", 0, 2, 2) }, func() (int, error) { return oracle.add("B", 0, 2, 2) }},
		{"Add C", func() (int, error) { return tb.AddBackend("C", 3, 1, 1) }, func() (int, error) { return oracle.add("C", 3, 1, 1) }},
		// Migration is mid-flight (Lim=1, several optional slots pending)
		// when the set changes again.
		{"Add D mid-migration", func() (int, error) { return tb.AddBackend("D", 5, 3, 2) }, func() (int, error) { return oracle.add("D", 5, 3, 2) }},
		{"Remove B mid-migration", func() (int, error) { return tb.RemoveBackend("B") }, func() (int, error) { return oracle.remove("B") }},
		{"Step", func() (int, error) { return tb.Step(), nil }, func() (int, error) { return oracle.step(), nil }},
		{"Add E mid-migration", func() (int, error) { return tb.AddBackend("E", 1, 1, 1) }, func() (int, error) { return oracle.add("E", 1, 1, 1) }},
		{"Remove A mid-migration", func() (int, error) { return tb.RemoveBackend("A") }, func() (int, error) { return oracle.remove("A") }},
	}
	for i, o := range ops {
		gotN, gotErr := o.run()
		wantN, wantErr := o.want()
		if gotN != wantN || !errors.Is(gotErr, wantErr) {
			t.Fatalf("op %d %s = (%d, %v), oracle = (%d, %v)", i, o.desc, gotN, gotErr, wantN, wantErr)
		}
		if got, want := tb.Pending(), oracle.pending(); got != want {
			t.Fatalf("op %d %s: Pending = %d, oracle = %d", i, o.desc, got, want)
		}
		gotTab := tb.Table()
		for s := range gotTab {
			if gotTab[s] != oracle.cur[s] {
				t.Fatalf("op %d %s: table = %v, oracle = %v", i, o.desc, gotTab, oracle.cur)
			}
		}
		t.Logf("op %d %-22s changed=%d pending=%d table=%v (matches naive step-by-step oracle)",
			i, o.desc, gotN, tb.Pending(), gotTab)
	}
	drain(t, tb)
	for tb.Pending() == 0 && oracle.pending() != 0 {
		oracle.step()
	}
	wantTable(t, tb, oracle.cur)
}

// TestReplayDeterminism: the same operation sequence replayed on two fresh
// tables yields identical return values and identical tables.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const m, lim = 17, 3
	names := []string{"n0", "n1", "n2", "n3", "n4"}
	type operation struct {
		kind                 int // 0 add, 1 remove, 2 step
		name                 string
		offset, skip, weight int
	}
	seq := make([]operation, 60)
	for i := range seq {
		seq[i] = operation{
			kind:   rng.Intn(3),
			name:   names[rng.Intn(len(names))],
			offset: rng.Intn(m),
			skip:   1 + rng.Intn(m-1),
			weight: 1 + rng.Intn(16),
		}
	}
	run := func() ([]int, []string) {
		tb := mustNew(t, m, lim)
		results := make([]int, 0, len(seq))
		for _, o := range seq {
			switch o.kind {
			case 0:
				n, _ := tb.AddBackend(o.name, o.offset, o.skip, o.weight)
				results = append(results, n)
			case 1:
				n, _ := tb.RemoveBackend(o.name)
				results = append(results, n)
			case 2:
				results = append(results, tb.Step())
			}
		}
		return results, tb.Table()
	}
	r1, tab1 := run()
	r2, tab2 := run()
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("op %d: replay mismatch %d != %d", i, r1[i], r2[i])
		}
	}
	for s := range tab1 {
		if tab1[s] != tab2[s] {
			t.Fatalf("slot %d: replay mismatch %q != %q", s, tab1[s], tab2[s])
		}
	}
	t.Logf("replayed %d ops twice: %d return values and final table %v identical", len(seq), len(r1), tab1)
}
