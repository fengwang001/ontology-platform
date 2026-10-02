package cinema

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveRegistry is a deliberately simple reference implementation: it
// enumerates every row and every segment exactly as the spec reads,
// scans every hold for every seat, and sorts candidates explicitly.
type naiveRegistry struct {
	R, W   int
	T      int64
	maxNow int64
	hasNow bool
	nextID int
	holds  map[int]*naiveHold
}

type naiveHold struct {
	row, start, k   int
	created, expiry int64
	state           holdState
	closed          int64
}

func newNaive(R, W int, T int64) *naiveRegistry {
	if R < 1 || R > 26 || W < 1 || W > 40 || T < 1 {
		panic("naive: invalid dimensions")
	}
	return &naiveRegistry{R: R, W: W, T: T, nextID: 1, holds: make(map[int]*naiveHold)}
}

func (n *naiveRegistry) checkClock(now int64) error {
	if n.hasNow && now < n.maxNow {
		return ErrClockRegression
	}
	return nil
}

// occupied reports whether the hold covers its seats at now, using the
// same event model as the registry under test.
func (h *naiveHold) occupied(now int64) bool {
	if now < h.created {
		return false
	}
	switch h.state {
	case holdConfirmed:
		if now >= h.closed {
			return true
		}
	case holdReleased:
		if now >= h.closed {
			return false
		}
	}
	return now < h.expiry
}

// seatTaken scans every hold for every seat: the dumbest possible check.
func (n *naiveRegistry) seatTaken(row, seat int, now int64) bool {
	for _, h := range n.holds {
		if h.row == row && seat >= h.start && seat < h.start+h.k && h.occupied(now) {
			return true
		}
	}
	return false
}

// naiveCandidate is one fully free segment [s, s+k-1] of one row.
type naiveCandidate struct {
	row, s, dev       int
	leftRun, rightRun int
	orphan            bool
}

func (n *naiveRegistry) candidates(k int, now int64) []naiveCandidate {
	var out []naiveCandidate
	for row := 1; row <= n.R; row++ {
		for s := 1; s+k-1 <= n.W; s++ {
			free := true
			for c := s; c < s+k; c++ {
				if n.seatTaken(row, c, now) {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			left := 0
			for c := s - 1; c >= 1 && !n.seatTaken(row, c, now); c-- {
				left++
			}
			right := 0
			for c := s + k; c <= n.W && !n.seatTaken(row, c, now); c++ {
				right++
			}
			dev := 2*s + k - 1 - (n.W + 1)
			if dev < 0 {
				dev = -dev
			}
			out = append(out, naiveCandidate{
				row: row, s: s, dev: dev,
				leftRun: left, rightRun: right,
				orphan: left == 1 || right == 1,
			})
		}
	}
	return out
}

// hold runs Hold on the naive implementation and also returns a
// human-readable rationale for the log.
func (n *naiveRegistry) hold(k int, now int64) (id, row, s int, rationale string, err error) {
	if err := n.checkClock(now); err != nil {
		return 0, 0, 0, "clock check failed", err
	}
	if k < 1 || k > 8 {
		return 0, 0, 0, "k outside [1,8]", ErrInvalidPartySize
	}
	all := n.candidates(k, now)
	var clean []naiveCandidate
	for _, c := range all {
		if !c.orphan {
			clean = append(clean, c)
		}
	}
	pool, pass := clean, 1
	if len(pool) == 0 {
		pool, pass = all, 2
	}
	if len(pool) == 0 {
		return 0, 0, 0, "no k-consecutive free segment in any row", ErrNoSeats
	}
	sort.Slice(pool, func(i, j int) bool {
		a, b := pool[i], pool[j]
		if a.row != b.row {
			return a.row < b.row
		}
		if a.dev != b.dev {
			return a.dev < b.dev
		}
		return a.s < b.s
	})
	best := pool[0]
	id = n.nextID
	n.nextID++
	n.holds[id] = &naiveHold{
		row: best.row, start: best.s, k: k,
		created: now, expiry: now + n.T,
	}
	n.maxNow, n.hasNow = now, true
	rationale = fmt.Sprintf("pass=%d candidates=%d clean=%d dev=%d leftRun=%d rightRun=%d orphan=%v",
		pass, len(all), len(clean), best.dev, best.leftRun, best.rightRun, best.orphan)
	return id, best.row, best.s, rationale, nil
}

func (n *naiveRegistry) resolve(id int, now int64) (*naiveHold, error) {
	h, ok := n.holds[id]
	if !ok {
		return nil, ErrHoldNotFound
	}
	if h.state == holdConfirmed {
		return nil, ErrHoldConfirmed
	}
	if h.state == holdReleased {
		return nil, ErrHoldReleased
	}
	if now >= h.expiry {
		return nil, ErrHoldExpired
	}
	return h, nil
}

func (n *naiveRegistry) confirm(id int, now int64) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	h, err := n.resolve(id, now)
	if err != nil {
		return err
	}
	h.state = holdConfirmed
	h.closed = now
	n.maxNow, n.hasNow = now, true
	return nil
}

func (n *naiveRegistry) release(id int, now int64) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	h, err := n.resolve(id, now)
	if err != nil {
		return err
	}
	h.state = holdReleased
	h.closed = now
	n.maxNow, n.hasNow = now, true
	return nil
}

func (n *naiveRegistry) seats(now int64) [][]Status {
	out := make([][]Status, n.R)
	for i := range out {
		out[i] = make([]Status, n.W)
	}
	for _, h := range n.holds {
		if !h.occupied(now) {
			continue
		}
		st := Held
		if h.state == holdConfirmed && now >= h.closed {
			st = Confirmed
		}
		for c := h.start; c < h.start+h.k; c++ {
			out[h.row-1][c-1] = st
		}
	}
	return out
}

// errName maps an error to a stable token for cross-implementation
// comparison.
func errName(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrClockRegression):
		return "clock"
	case errors.Is(err, ErrInvalidPartySize):
		return "party"
	case errors.Is(err, ErrNoSeats):
		return "noseats"
	case errors.Is(err, ErrHoldNotFound):
		return "notfound"
	case errors.Is(err, ErrHoldConfirmed):
		return "confirmed"
	case errors.Is(err, ErrHoldReleased):
		return "released"
	case errors.Is(err, ErrHoldExpired):
		return "expired"
	}
	return "unknown:" + err.Error()
}

// recordedOp is one random operation plus the registry's answer, kept
// so the exact sequence can be replayed for determinism.
type recordedOp struct {
	desc     string
	apply    func(r *Registry) string
	mainOut  string
	naiveOut string
}

// checkInvariants verifies the cross-seat invariants on the registry
// under test at the given now.
func checkInvariants(t *testing.T, r *Registry, now int64, R, W int) {
	t.Helper()
	// Hold ids are gapless: exactly 1..nextID-1 exist.
	if len(r.holds) != r.nextID-1 {
		t.Fatalf("hold count %d != nextID-1 %d", len(r.holds), r.nextID-1)
	}
	for id := 1; id < r.nextID; id++ {
		if _, ok := r.holds[id]; !ok {
			t.Fatalf("hold id %d missing (ids must be gapless)", id)
		}
	}
	// No seat is covered by two occupying holds at once.
	cover := make([][]int, R)
	for i := range cover {
		cover[i] = make([]int, W)
	}
	for _, h := range r.holds {
		if _, occ := h.statusAt(now); !occ {
			continue
		}
		for c := h.start; c < h.start+h.k; c++ {
			cover[h.row-1][c-1]++
			if cover[h.row-1][c-1] > 1 {
				t.Fatalf("seat (%d,%d) doubly occupied at now=%d", h.row, c, now)
			}
		}
	}
	// free+held+confirmed == R*W.
	total := 0
	for _, row := range r.Seats(now) {
		total += len(row)
	}
	if total != R*W {
		t.Fatalf("seat count %d != %d", total, R*W)
	}
}

// TestRandomSequencesMatchNaive replays 2000 random operation sequences
// against both the registry and the naive reference implementation and
// requires identical outcomes, then replays each sequence once more on
// a fresh registry to prove exact reproducibility.
func TestRandomSequencesMatchNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		R, W := 1+rng.Intn(6), 1+rng.Intn(10)
		if seq%7 == 0 { // some larger halls
			R, W = 1+rng.Intn(26), 1+rng.Intn(40)
		}
		T := int64(1 + rng.Intn(6))
		main, err := NewRegistry(R, W, T)
		if err != nil {
			t.Fatalf("seq %d: NewRegistry: %v", seq, err)
		}
		naive := newNaive(R, W, T)
		t.Logf("seq=%d R=%d W=%d T=%d", seq, R, W, T)

		var now int64
		var ops []recordedOp
		nOps := 20 + rng.Intn(20)
		for op := 0; op < nOps; op++ {
			// Mostly advance the clock, sometimes hold it, rarely
			// rewind it to trigger clock regressions.
			switch rng.Intn(10) {
			case 0:
				now -= int64(rng.Intn(4))
			case 1, 2:
			default:
				now += int64(rng.Intn(4))
			}
			var rec recordedOp
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4: // Hold
				k := rng.Intn(11) // 0..10: includes invalid sizes
				kn := now
				rec.desc = fmt.Sprintf("Hold(k=%d, now=%d)", k, kn)
				rec.apply = func(r *Registry) string {
					id, row, s, err := r.Hold(k, kn)
					return fmt.Sprintf("id=%d row=%d s=%d err=%s", id, row, s, errName(err))
				}
				idN, rowN, sN, why, errN := naive.hold(k, now)
				rec.naiveOut = fmt.Sprintf("id=%d row=%d s=%d err=%s", idN, rowN, sN, errName(errN))
				rec.desc += " | " + why
			case 5, 6, 7: // Confirm
				id := rng.Intn(naive.nextID + 3)
				kn := now
				rec.desc = fmt.Sprintf("Confirm(id=%d, now=%d)", id, kn)
				rec.apply = func(r *Registry) string {
					return "err=" + errName(r.Confirm(id, kn))
				}
				rec.naiveOut = "err=" + errName(naive.confirm(id, now))
			case 8: // Release
				id := rng.Intn(naive.nextID + 3)
				kn := now
				rec.desc = fmt.Sprintf("Release(id=%d, now=%d)", id, kn)
				rec.apply = func(r *Registry) string {
					return "err=" + errName(r.Release(id, kn))
				}
				rec.naiveOut = "err=" + errName(naive.release(id, now))
			default: // Seats (pure query, arbitrary now allowed)
				q := now + int64(rng.Intn(5)) - 2
				rec.desc = fmt.Sprintf("Seats(now=%d)", q)
				rec.apply = func(r *Registry) string {
					return gridString(r.Seats(q))
				}
				rec.naiveOut = gridString(naive.seats(q))
			}
			rec.mainOut = rec.apply(main)
			ops = append(ops, rec)
			t.Logf("seq=%d op=%02d in=%s | main=[%s] naive=[%s]", seq, op, rec.desc, rec.mainOut, rec.naiveOut)
			if rec.mainOut != rec.naiveOut {
				t.Fatalf("seq %d op %d %s:\nmain : %s\nnaive: %s", seq, op, rec.desc, rec.mainOut, rec.naiveOut)
			}
			checkInvariants(t, main, now, R, W)
		}

		// Replay the identical sequence on a fresh registry: the
		// outputs must be bit-for-bit identical.
		fresh, err := NewRegistry(R, W, T)
		if err != nil {
			t.Fatalf("seq %d: NewRegistry (replay): %v", seq, err)
		}
		for op, rec := range ops {
			if got := rec.apply(fresh); got != rec.mainOut {
				t.Fatalf("seq %d op %d %s: replay=%s first=%s", seq, op, rec.desc, got, rec.mainOut)
			}
		}
	}
}

func gridString(g [][]Status) string {
	out := ""
	for _, row := range g {
		for _, st := range row {
			switch st {
			case Free:
				out += "."
			case Held:
				out += "h"
			case Confirmed:
				out += "c"
			}
		}
		out += "/"
	}
	return out
}
