package maglev

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// naiveRef is an intentionally straightforward re-implementation of every
// rule from the specification, written independently from the production code.
type naiveRef struct {
	m, lim int
	be     map[string][3]int // name -> {offset, skip, weight}
	cur    []string
}

func newNaive(m, lim int) *naiveRef {
	return &naiveRef{m: m, lim: lim, be: map[string][3]int{}, cur: make([]string, m)}
}

func (r *naiveRef) target() []string {
	t := make([]string, r.m)
	names := make([]string, 0, len(r.be))
	for n := range r.be {
		names = append(names, n)
	}
	sortStrings(names)
	if len(names) == 0 {
		return t
	}

	// explicit permutation tables and pointers, built exactly as specified
	perms := make([][]int, len(names))
	ptr := make([]int, len(names))
	for i, n := range names {
		cfg := r.be[n]
		p := make([]int, r.m)
		for j := 0; j < r.m; j++ {
			p[j] = (cfg[0] + j*cfg[1]) % r.m
		}
		perms[i] = p
	}

	filled := 0
	for filled < r.m {
		madeProgress := false
		for i := 0; i < len(names) && filled < r.m; i++ {
			w := r.be[names[i]][2]
			for k := 0; k < w && filled < r.m; k++ {
				for {
					if ptr[i] >= r.m {
						// unreachable when M is prime and 1<=skip<M: the
						// permutation covers every slot, so an empty one must
						// be found within M probes.
						panic(fmt.Sprintf("naive: backend %s exhausted permutation with %d slots open",
							names[i], r.m-filled))
					}
					slot := perms[i][ptr[i]]
					ptr[i]++
					if t[slot] == "" {
						t[slot] = names[i]
						filled++
						madeProgress = true
						break
					}
				}
			}
		}
		if !madeProgress {
			panic(fmt.Sprintf("naive: no progress filling target; backends=%v target=%v",
				r.be, t))
		}
	}
	return t
}

// migrate changes mandatory slots first (unlimited), then up to lim optional
// slots in ascending slot order; returns the number of changed slots.
func (r *naiveRef) migrate() int {
	tg := r.target()
	changed := 0
	for s := 0; s < r.m; s++ {
		_, live := r.be[r.cur[s]]
		if r.cur[s] == "" || !live {
			if r.cur[s] != tg[s] {
				r.cur[s] = tg[s]
				changed++
			}
		}
	}
	used := 0
	for s := 0; s < r.m && used < r.lim; s++ {
		if r.cur[s] != tg[s] {
			r.cur[s] = tg[s]
			changed++
			used++
		}
	}
	return changed
}

func (r *naiveRef) step() int {
	if len(r.be) == 0 {
		return 0
	}
	tg := r.target()
	changed, used := 0, 0
	for s := 0; s < r.m && used < r.lim; s++ {
		if r.cur[s] != tg[s] {
			r.cur[s] = tg[s]
			changed++
			used++
		}
	}
	return changed
}

func (r *naiveRef) pending() int {
	tg := r.target()
	n := 0
	for s := range r.cur {
		if r.cur[s] != tg[s] {
			n++
		}
	}
	return n
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// op is one recorded operation so the same sequence can be replayed.
type op struct {
	kind                       string // add / remove / step / lookup
	name                       string
	offset, skip, weight, hash int
}

func TestRandomDifferential2000(t *testing.T) {
	runDifferential(t, 2000, false)
}

// Small run with per-operation logging of inputs, outputs and the decision
// basis; view it with `go test -v -run TestRandomDifferentialLogged`.
func TestRandomDifferentialLogged(t *testing.T) {
	runDifferential(t, 30, true)
}

func runDifferential(t *testing.T, iters int, verbose bool) {
	primes := []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37}
	rng := rand.New(rand.NewPCG(0x517cc1b727220a95, 0x9e3779b97f4a7c15))

	for iter := 0; iter < iters; iter++ {
		m := primes[rng.IntN(len(primes))]
		lim := 1 + rng.IntN(m)
		tb, err := New(m, lim)
		if err != nil {
			t.Fatalf("iter %d: New(%d,%d): %v", iter, m, lim, err)
		}
		ref := newNaive(m, lim)

		var ops []op
		type result struct {
			n     int
			errOK bool
			table []string
		}
		var recorded []result
		steps := 20 + rng.IntN(40)
		for k := 0; k < steps; k++ {
			// biased choice keeps add/remove/step/lookup all exercised
			var o op
			switch rng.IntN(10) {
			case 0, 1, 2, 3: // add, with frequent invalid inputs
				o.kind = "add"
				if rng.IntN(5) == 0 {
					o.name = "" // invalid: empty name
				} else {
					o.name = fmt.Sprintf("n%d", rng.IntN(m+2))
				}
				o.offset = rng.IntN(m + 2)
				o.skip = 1 + rng.IntN(m+1) // may become == m (invalid)
				if rng.IntN(8) == 0 {
					o.skip = 0
				}
				o.weight = 1 + rng.IntN(18) // may exceed 16
			case 4, 5:
				o.kind = "remove"
				if len(ref.be) > 0 && rng.IntN(3) > 0 {
					var pool []string
					for n := range ref.be {
						pool = append(pool, n)
					}
					o.name = pool[rng.IntN(len(pool))]
				} else {
					o.name = "ghost"
				}
			case 6, 7, 8:
				o.kind = "step"
			default:
				o.kind = "lookup"
				o.hash = int(rng.Uint64() >> 1)
			}
			ops = append(ops, o)

			got, gotErr := runOp(tb, o)
			want, wantErr := runRef(ref, o)
			gotTable := tb.Table()
			rec := result{
				n:     got,
				errOK: gotErr != nil,
				table: append([]string(nil), gotTable...),
			}
			gotPending := tb.Pending()
			refPending := ref.pending()
			tablesAgree := reflect.DeepEqual(gotTable, ref.cur)
			if verbose {
				basis := fmt.Sprintf("changedSlots agree=%v pending agree=%v table agree=%v",
					got == want, gotPending == refPending, tablesAgree)
				t.Logf("iter=%d m=%d lim=%d op=%s -> got(n=%d err=%v table=%s) ref(n=%d err=%v table=%s) judge: %s",
					iter, m, lim, formatOp(o), got, gotErr, strings.Join(gotTable, "."),
					want, wantErr, strings.Join(ref.cur, "."), basis)
			}

			if got != want || !sameErr(gotErr, wantErr) {
				t.Errorf("iter %d op %s: got n=%d err=%v, want n=%d err=%v",
					iter, formatOp(o), got, gotErr, want, wantErr)
			}
			if gotPending != refPending {
				t.Errorf("iter %d after %s: pending got=%d ref=%d",
					iter, formatOp(o), gotPending, refPending)
			}
			if !tablesAgree {
				t.Errorf("iter %d after %s:\n got %v\nwant %v",
					iter, formatOp(o), gotTable, ref.cur)
			}
			if t.Failed() {
				t.Fatalf("iter %d op %s: got n=%d err=%v, want n=%d err=%v",
					iter, formatOp(o), got, gotErr, want, wantErr)
			}
			recorded = append(recorded, rec)
		}

		// replay: identical operation sequence must produce identical results
		replay, _ := New(m, lim)
		for i, o := range ops {
			n2, e2 := runOp(replay, o)
			rec := recorded[i]
			if n2 != rec.n || ((e2 != nil) != (rec.errOK)) ||
				!reflect.DeepEqual(replay.Table(), rec.table) {
				rt := replay.Table()
				t.Fatalf("iter %d replay mismatch on %s: n2=%d rec.n=%d lens=%d/%d deepEmpty=%v\n replay=%q\nrecord=%q",
					iter, formatOp(o), n2, rec.n, len(rt), len(rec.table),
					reflect.DeepEqual(rt, make([]string, m)), rt, rec.table)
			}
		}
		if !reflect.DeepEqual(tb.Table(), replay.Table()) {
			t.Fatalf("iter %d replay: final tables differ", iter)
		}

		// every step must reduce pending by exactly min(lim, pending), and the
		// table converges within ceil(M/lim) steps
		maxSteps := (m + lim - 1) / lim
		taken := 0
		for tb.Pending() > 0 {
			before := tb.Pending()
			want := min2(lim, before)
			if n := tb.Step(); n != want || tb.Pending() != before-want {
				t.Fatalf("iter %d: step n=%d pending %d->%d, want n=%d ->%d",
					iter, n, before, tb.Pending(), want, before-want)
			}
			taken++
			if taken > maxSteps {
				t.Fatalf("iter %d: more than ceil(M/lim)=%d steps", iter, maxSteps)
			}
		}
		final := tb.Table()
		for s, owner := range final {
			if len(tb.backends) == 0 {
				if owner != "" {
					t.Fatalf("iter %d: empty set but slot %d = %q", iter, s, owner)
				}
			} else if _, ok := tb.backends[owner]; !ok {
				t.Fatalf("iter %d: slot %d owned by non-member %q", iter, s, owner)
			}
		}
	}
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func runOp(tb *Table, o op) (int, error) {
	switch o.kind {
	case "add":
		return tb.AddBackend(o.name, o.offset, o.skip, o.weight)
	case "remove":
		return tb.RemoveBackend(o.name)
	case "step":
		return tb.Step(), nil
	default:
		_, err := tb.Lookup(uint64(o.hash))
		return 0, err
	}
}

func runRef(r *naiveRef, o op) (int, error) {
	switch o.kind {
	case "add":
		if o.name == "" {
			return 0, ErrEmptyName
		}
		if o.offset < 0 || o.offset >= r.m || o.skip < 1 || o.skip >= r.m ||
			o.weight < 1 || o.weight > 16 {
			return 0, ErrInvalidBackend
		}
		if _, ok := r.be[o.name]; ok {
			return 0, ErrBackendExists
		}
		if len(r.be) >= r.m {
			return 0, ErrTableFull
		}
		r.be[o.name] = [3]int{o.offset, o.skip, o.weight}
		return r.migrate(), nil
	case "remove":
		if _, ok := r.be[o.name]; !ok {
			return 0, ErrBackendNotFound
		}
		delete(r.be, o.name)
		return r.migrate(), nil
	case "step":
		return r.step(), nil
	default:
		if len(r.be) == 0 {
			return 0, ErrNoBackends
		}
		return 0, nil
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errorsIs(a, b)
}

func errorsIs(a, b error) bool {
	switch b {
	case ErrEmptyName, ErrInvalidBackend, ErrBackendExists, ErrTableFull,
		ErrBackendNotFound, ErrNoBackends:
		return a == b
	}
	return a.Error() == b.Error()
}

func formatOp(o op) string {
	switch o.kind {
	case "add":
		return fmt.Sprintf("add(%q,%d,%d,%d)", o.name, o.offset, o.skip, o.weight)
	case "remove":
		return fmt.Sprintf("remove(%q)", o.name)
	case "lookup":
		return fmt.Sprintf("lookup(%d)", o.hash)
	default:
		return "step()"
	}
}
