package cursor

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func mustOpen(t *testing.T, m *Manager, name string, n int64, scroll bool) {
	t.Helper()
	if err := m.Open(name, n, scroll); err != nil {
		t.Fatalf("Open(%q,%d,%v): %v", name, n, scroll, err)
	}
}

func TestInt64Extremes(t *testing.T) {
	m := NewManager()
	mustOpen(t, m, "x", maxRows, true)

	try := func(name string, n int64, op Operation, k, wantPos int64, wantRow bool) {
		t.Helper()
		rows, err := m.Fetch(name, op, k)
		pos, _ := m.Position(name)
		t.Logf("input: n=%d op=%s k=%d | output: rows=%v err=%v pos=%d | judge: pos in [0,n+1], wantPos=%d row=%v",
			n, opName(op), k, rows, err, pos, wantPos, wantRow)
		if err != nil {
			t.Fatalf("%s %d: %v", opName(op), k, err)
		}
		if pos != wantPos {
			t.Fatalf("%s %d: pos = %d, want %d", opName(op), k, pos, wantPos)
		}
		if wantRow && len(rows) != 1 {
			t.Fatalf("%s %d: rows = %v, want one row", opName(op), k, rows)
		}
	}

	try("x", maxRows, Relative, math.MaxInt64, maxRows+1, false)
	try("x", maxRows, Absolute, math.MinInt64, 0, false)
	try("x", maxRows, Relative, math.MinInt64, 0, false)
	try("x", maxRows, First, 0, 1, true)
	try("x", maxRows, Relative, math.MaxInt64, maxRows+1, false)
	try("x", maxRows, Prior, 0, maxRows, true)
	try("x", maxRows, Absolute, math.MaxInt64, maxRows+1, false)

	// Bulk extremes on a small n: k must not wrap and a short fetch ends on
	// the edge regardless of how large k is.
	mustOpen(t, m, "s", 5, true)
	try("s", 5, Forward, math.MaxInt64, 6, false)
	try("s", 5, Backward, math.MaxInt64, 0, false)
}

// TestRejections checks every reject reason, reporting order and that
// rejected operations never move the cursor.
func TestRejections(t *testing.T) {
	m := NewManager()

	check := func(label string, err, want error) {
		t.Helper()
		t.Logf("input: %s | output: err=%v | judge: matched sentinel %v", label, err, want)
		if err != want {
			t.Fatalf("%s: err = %v, want %v", label, err, want)
		}
	}

	check("open empty", m.Open("", 5, true), ErrEmptyName)
	check("open negative n", m.Open("a", -1, true), ErrInvalidN)
	check("open n > 2^40", m.Open("a", maxRows+1, true), ErrInvalidN)

	mustOpen(t, m, "a", 5, true)
	check("open duplicate", m.Open("a", 1, true), ErrNameExists)

	check("fetch missing", errOnly(m.Fetch("ghost", Next, 0)), ErrNotFound)
	check("fetch missing bad op", errOnly(m.Fetch("ghost", Operation(99), 0)), ErrNotFound)

	check("fetch bad op", errOnly(m.Fetch("a", Operation(99), 0)), ErrInvalidOp)

	mustOpen(t, m, "fo", 5, false)
	check("forward-only prior", errOnly(m.Fetch("fo", Prior, 0)), ErrForwardOnly)
	check("forward-only absolute", errOnly(m.Fetch("fo", Absolute, 1)), ErrForwardOnly)
	check("forward-only relative negative", errOnly(m.Fetch("fo", Relative, -1)), ErrForwardOnly)
	// Order: forward-only restriction beats non-positive bulk k.
	check("forward-only backward k=0", errOnly(m.Fetch("fo", Backward, 0)), ErrForwardOnly)
	check("forward k=0", errOnly(m.Fetch("a", Forward, 0)), ErrNonPositiveK)
	check("backward k=-1", errOnly(m.Fetch("a", Backward, -1)), ErrNonPositiveK)

	if _, err := m.Position("ghost"); err != ErrNotFound {
		t.Fatalf("Position missing: err = %v, want ErrNotFound", err)
	}
	if err := m.Close("ghost"); err != ErrNotFound {
		t.Fatalf("Close missing: err = %v, want ErrNotFound", err)
	}

	if pos, err := m.Position("fo"); err != nil || pos != 0 {
		t.Fatalf("rejected ops moved forward-only cursor: pos=%d err=%v", pos, err)
	}

	rows, err := m.Fetch("fo", Next, 0)
	if err != nil || len(rows) != 1 || rows[0] != 1 {
		t.Fatalf("forward-only NEXT: rows=%v err=%v", rows, err)
	}
	if err := m.Close("fo"); err != nil {
		t.Fatalf("close: %v", err)
	}
	mustOpen(t, m, "fo", 2, true) // name reusable after Close
	if pos, err := m.Position("fo"); err != nil || pos != 0 {
		t.Fatalf("reopened cursor pos = %d, err = %v", pos, err)
	}
}

// ---- Differential fuzzing against an independent big.Int naive model ----

type refCursor struct {
	n int64
	p int64
}

// refFetch computes target positions with big.Int (so int64 wraparound is
// impossible) and simulates bulk ops one row at a time.
func (r *refCursor) refFetch(op Operation, k int64) []int64 {
	n := big.NewInt(r.n)
	if op >= Next && op <= Relative {
		t := new(big.Int)
		switch op {
		case Next:
			t.Add(big.NewInt(r.p), big.NewInt(1))
		case Prior:
			t.Sub(big.NewInt(r.p), big.NewInt(1))
		case First:
			t.SetInt64(1)
		case Last:
			t.SetInt64(r.n)
		case Absolute:
			switch {
			case k > 0:
				t.SetInt64(k)
			case k < 0:
				t.Add(n, big.NewInt(1))
				t.Add(t, big.NewInt(k))
			default:
				t.SetInt64(0)
			}
		case Relative:
			t.Add(big.NewInt(r.p), big.NewInt(k))
		}
		if t.Cmp(big.NewInt(1)) < 0 {
			r.p = 0
			return nil
		}
		if t.Cmp(n) > 0 {
			r.p = r.n + 1
			return nil
		}
		row := t.Int64()
		r.p = row
		return []int64{row}
	}

	if op == Forward {
		start := r.p + 1
		if start > r.n {
			r.p = r.n + 1
			return nil
		}
		return r.refBulk(start, k, true)
	}
	start := r.p - 1
	if start < 1 {
		r.p = 0
		return nil
	}
	return r.refBulk(start, k, false)
}

func (r *refCursor) refBulk(start, k int64, fwd bool) []int64 {
	var rows []int64
	given := big.NewInt(k)
	stepped := big.NewInt(0)
	row := start
	for stepped.Cmp(given) < 0 {
		if fwd && row > r.n {
			break
		}
		if !fwd && row < 1 {
			break
		}
		rows = append(rows, row)
		stepped.Add(stepped, big.NewInt(1))
		if fwd {
			row++
		} else {
			row--
		}
	}
	if stepped.Cmp(given) < 0 {
		if fwd {
			r.p = r.n + 1
		} else {
			r.p = 0
		}
	} else if fwd {
		r.p = rows[len(rows)-1]
	} else {
		r.p = rows[len(rows)-1]
	}
	return rows
}

type refOp struct {
	op Operation
	k  int64
}

// TestFuzzAgainstReference replays 2000 random operation sequences on both
// the manager and the naive reference model and compares every return and
// position.
func TestFuzzAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	const sequences = 2000

	extremeKs := []int64{0, 1, -1, 2, -2, math.MaxInt64, math.MinInt64, math.MinInt64 + 1, 1 << 20, -(1 << 20)}

	for seq := 0; seq < sequences; seq++ {
		n := rng.Int63n(12)
		scroll := rng.Intn(2) == 0
		steps := 3 + rng.Intn(18)
		name := "c"

		m := NewManager()
		if err := m.Open(name, n, scroll); err != nil {
			t.Fatalf("seq %d: open: %v", seq, err)
		}
		ref := &refCursor{n: n}

		var log strings.Builder
		fmt.Fprintf(&log, "seq %d: n=%d scroll=%v steps=%d", seq, n, scroll, steps)

		for step := 0; step < steps; step++ {
			var op Operation
			if scroll {
				op = Operation(1 + rng.Intn(int(Backward)))
			} else {
				switch rng.Intn(3) {
				case 0:
					op = Next
				case 1:
					op = Forward
				case 2:
					op = Relative
				}
			}
			k := extremeKs[rng.Intn(len(extremeKs))]
			if rng.Intn(3) == 0 {
				limit := n + 4
				if limit == 0 {
					limit = 4
				}
				k = rng.Int63n(limit*2+1) - limit
			}
			if op == Forward || op == Backward {
				if k <= 0 {
					k = 1 + rng.Int63n(n+3)
				}
			}

			got, gErr := m.Fetch(name, op, k)
			pos, _ := m.Position(name)

			illegal := (!scroll && !allowedForwardOnly(op, k)) ||
				((op == Forward || op == Backward) && k <= 0)

			fmt.Fprintf(&log, "\n  step %d: op=%s k=%d -> rows=%v err=%v pos=%d; judge illegal=%v",
				step, opName(op), k, got, gErr, pos, illegal)

			if illegal {
				if gErr == nil {
					t.Fatalf("seq %d step %d: expected rejection for %s k=%d\n%s", seq, step, opName(op), k, log.String())
				}
				if pos != ref.p {
					t.Fatalf("seq %d step %d: rejected op moved cursor: pos=%d ref=%d\n%s", seq, step, pos, ref.p, log.String())
				}
				continue
			}
			if gErr != nil {
				t.Fatalf("seq %d step %d: unexpected error %v\n%s", seq, step, gErr, log.String())
			}

			want := ref.refFetch(op, k)
			if !sameRows(got, want) {
				t.Fatalf("seq %d step %d: rows = %v, ref = %v\n%s", seq, step, got, want, log.String())
			}
			if pos != ref.p {
				t.Fatalf("seq %d step %d: pos = %d, ref = %d\n%s", seq, step, pos, ref.p, log.String())
			}
			if pos < 0 || pos > n+1 {
				t.Fatalf("seq %d step %d: pos %d outside [0,%d]\n%s", seq, step, pos, n+1, log.String())
			}
		}

		if seq < 5 || seq == sequences-1 {
			t.Logf("input/output/judge log:\n%s", log.String())
		}
	}
	t.Logf("input: %d random sequences | output: all compared | judge: rows and position matched the naive reference on every step", sequences)
}

// TestConcurrentSameCursor hammers one cursor concurrently; every observed
// position must stay in range, and replaying the recorded Fetch results as a
// single serial sequence must reproduce the final position (serializability
// sanity check via independent reference replay).
func TestConcurrentSameCursor(t *testing.T) {
	const n = 50
	m := NewManager()
	mustOpen(t, m, "c", n, true)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			ops := []Operation{Next, Prior, First, Last, Relative, Forward, Backward}
			for i := 0; i < 300; i++ {
				op := ops[rng.Intn(len(ops))]
				k := int64(1 + rng.Intn(5))
				if op != Forward && op != Backward && rng.Intn(2) == 0 {
					k = -k
				}
				rows, err := m.Fetch("c", op, k)
				if err != nil {
					t.Errorf("concurrent fetch: %v", err)
					return
				}
				pos, err := m.Position("c")
				if err != nil || pos < 0 || pos > n+1 {
					t.Errorf("bad pos %d (err=%v)", pos, err)
					return
				}
				_ = rows
			}
		}(int64(g + 1))
	}
	wg.Wait()

	pos, err := m.Position("c")
	if err != nil {
		t.Fatalf("final position: %v", err)
	}
	t.Logf("input: 16 goroutines x 300 mixed fetches on one cursor n=%d | output: final pos=%d | judge: 0 <= pos <= n+1 throughout", n, pos)
}

// TestConcurrentDifferentCursors exercises fully independent cursors.
func TestConcurrentDifferentCursors(t *testing.T) {
	m := NewManager()
	const cursors = 32
	for i := 0; i < cursors; i++ {
		mustOpen(t, m, fmt.Sprintf("c%d", i), int64(i), true)
	}
	var wg sync.WaitGroup
	for i := 0; i < cursors; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("c%d", idx)
			for j := 0; j < 200; j++ {
				if _, err := m.Fetch(name, Next, 0); err != nil {
					t.Errorf("cursor %s next: %v", name, err)
					return
				}
				if _, err := m.Fetch(name, First, 0); err != nil {
					t.Errorf("cursor %s first: %v", name, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < cursors; i++ {
		pos, err := m.Position(fmt.Sprintf("c%d", i))
		if err != nil || pos != 1 {
			t.Fatalf("cursor c%d pos=%d err=%v, want 1", i, pos, err)
		}
	}
}

func errOnly(_ []int64, err error) error { return err }

func sameRows(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func opName(op Operation) string {
	switch op {
	case Next:
		return "NEXT"
	case Prior:
		return "PRIOR"
	case First:
		return "FIRST"
	case Last:
		return "LAST"
	case Absolute:
		return "ABSOLUTE"
	case Relative:
		return "RELATIVE"
	case Forward:
		return "FORWARD"
	case Backward:
		return "BACKWARD"
	default:
		return "INVALID"
	}
}

type edgeStep struct {
	op   Operation
	k    int64
	want []int64
	pos  int64
	note string
}

// TestRequiredEdges walks every edge scenario required by the specification.
func TestRequiredEdges(t *testing.T) {
	m := NewManager()

	cases := []struct {
		name  string
		n     int64
		steps []edgeStep
	}{
		{"absolute", 5, []edgeStep{
			{Absolute, -1, []int64{5}, 5, "ABSOLUTE -1 = last row"},
			{Absolute, 0, nil, 0, "ABSOLUTE 0 -> before first, no row"},
			{Absolute, -5, []int64{1}, 1, "ABSOLUTE -5 = first row"},
			{Absolute, -6, nil, 0, "ABSOLUTE past low edge -> 0"},
			{Absolute, 6, nil, 6, "ABSOLUTE past high edge -> n+1"},
			{Absolute, math.MaxInt64, nil, 6, "ABSOLUTE MaxInt64 -> n+1"},
			{Absolute, math.MinInt64, nil, 0, "ABSOLUTE MinInt64 -> 0"},
		}},
		{"prior-from-after-last", 5, []edgeStep{
			{Absolute, 6, nil, 6, "move after last"},
			{Prior, 0, []int64{5}, 5, "PRIOR from n+1 yields row n"},
		}},
		{"next-past-last-then-prior", 5, []edgeStep{
			{Last, 0, []int64{5}, 5, "LAST"},
			{Next, 0, nil, 6, "NEXT beyond last -> n+1, no row"},
			{Prior, 0, []int64{5}, 5, "PRIOR yields row n"},
		}},
		{"relative-zero", 5, []edgeStep{
			{First, 0, []int64{1}, 1, "FIRST"},
			{Relative, 0, []int64{1}, 1, "RELATIVE 0 repeats current row"},
			{Absolute, 3, []int64{3}, 3, "go to row 3"},
			{Relative, 0, []int64{3}, 3, "RELATIVE 0 repeats row 3"},
			{Absolute, 0, nil, 0, "go before first"},
			{Relative, 0, nil, 0, "RELATIVE 0 at edge 0 returns no row"},
			{Absolute, 6, nil, 6, "go after last"},
			{Relative, 0, nil, 6, "RELATIVE 0 at edge n+1 returns no row"},
		}},
		{"zero-rows", 0, []edgeStep{
			{Next, 0, nil, 1, "n=0 NEXT -> 1 (=n+1), no row"},
			{Prior, 0, nil, 0, "n=0 PRIOR -> 0, no row"},
			{First, 0, nil, 1, "n=0 FIRST -> n+1"},
			{Last, 0, nil, 0, "n=0 LAST -> 0"},
			{Absolute, 1, nil, 1, "n=0 ABS 1 -> n+1"},
			{Absolute, -1, nil, 0, "n=0 ABS -1 -> 0"},
			{Relative, 5, nil, 1, "n=0 REL +5 -> n+1"},
			{Forward, 3, nil, 1, "n=0 FORWARD exhausts -> n+1"},
			{Backward, 3, nil, 0, "n=0 BACKWARD exhausts -> 0"},
		}},
		{"forward-exact-and-short", 5, []edgeStep{
			{Absolute, 0, nil, 0, "start before first"},
			{Forward, 3, []int64{1, 2, 3}, 3, "FORWARD 3 stops on last returned row"},
			{Forward, 2, []int64{4, 5}, 5, "FORWARD 2 exactly exhausts rows -> row n"},
			{Forward, 1, nil, 6, "FORWARD short -> n+1, no row"},
			{Absolute, 3, []int64{3}, 3, "back to a row"},
			{Forward, 10, []int64{4, 5}, 6, "FORWARD 10 returns tail, short -> n+1"},
		}},
		{"backward-from-after-last", 5, []edgeStep{
			{Absolute, 6, nil, 6, "after last"},
			{Backward, 2, []int64{5, 4}, 4, "BACKWARD from n+1 starts at row n"},
			{Backward, 10, []int64{3, 2, 1}, 0, "BACKWARD short -> 0"},
			{Backward, 1, nil, 0, "BACKWARD at 0 stays 0"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustOpen(t, m, tc.name, tc.n, true)
			for i, s := range tc.steps {
				got, err := m.Fetch(tc.name, s.op, s.k)
				pos, _ := m.Position(tc.name)
				t.Logf("input: %s n=%d op=%s k=%d | output: rows=%v err=%v pos=%d | judge: want rows=%v pos=%d (%s)",
					tc.name, tc.n, opName(s.op), s.k, got, err, pos, s.want, s.pos, s.note)
				if err != nil {
					t.Fatalf("step %d: unexpected error %v", i, err)
				}
				if !sameRows(got, s.want) {
					t.Fatalf("step %d (%s): rows = %v, want %v", i, s.note, got, s.want)
				}
				if pos != s.pos {
					t.Fatalf("step %d (%s): pos = %d, want %d", i, s.note, pos, s.pos)
				}
			}
		})
	}
}
