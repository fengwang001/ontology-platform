package tracker

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Naive per-character model used as a differential oracle. The document is
// a list of unique character tokens; every point anchor clings to one token
// (or a start/end sentinel) and its gap is recomputed from that token's
// index after each edit.

const (
	tokStart = -1 // sentinel before the first character
	tokEnd   = -2 // sentinel after the last character
)

type naivePoint struct {
	tok  int
	left bool // Left: gap right after tok; Right: gap right before tok
}

type naiveAnchor struct {
	isRange bool
	kind    RangeKind
	p       naivePoint // point anchor
	s, e    naivePoint // range endpoints
}

type naiveState struct {
	toks    []int
	anchors map[int]naiveAnchor
}

type naiveSim struct {
	toks    []int
	nextTok int
	anchors map[int]naiveAnchor
	history []naiveState // history[r] = state after r edits
}

func newNaiveSim(n0 int) *naiveSim {
	s := &naiveSim{anchors: map[int]naiveAnchor{}}
	for i := 0; i < n0; i++ {
		s.toks = append(s.toks, i)
	}
	s.nextTok = n0
	s.snapshot()
	return s
}

func (s *naiveSim) snapshot() {
	a := make(map[int]naiveAnchor, len(s.anchors))
	for k, v := range s.anchors {
		a[k] = v
	}
	t := append([]int(nil), s.toks...)
	s.history = append(s.history, naiveState{toks: t, anchors: a})
}

// syncAnchors reflects anchor add/remove (which do not bump the revision)
// into the current revision's snapshot.
func (s *naiveSim) syncAnchors() {
	a := make(map[int]naiveAnchor, len(s.anchors))
	for k, v := range s.anchors {
		a[k] = v
	}
	s.history[len(s.history)-1].anchors = a
}

func indexOfTok(toks []int, tok int) int {
	for i, v := range toks {
		if v == tok {
			return i
		}
	}
	return -1
}

func gapOf(toks []int, pt naivePoint) int {
	if pt.left {
		if pt.tok == tokStart {
			return 0
		}
		return indexOfTok(toks, pt.tok) + 1
	}
	if pt.tok == tokEnd {
		return len(toks)
	}
	return indexOfTok(toks, pt.tok)
}

func attach(toks []int, x int, b Bias) naivePoint {
	if b == Left {
		if x == 0 {
			return naivePoint{tok: tokStart, left: true}
		}
		return naivePoint{tok: toks[x-1], left: true}
	}
	if x == len(toks) {
		return naivePoint{tok: tokEnd}
	}
	return naivePoint{tok: toks[x]}
}

func (s *naiveSim) addPoint(id, x int, b Bias) {
	s.anchors[id] = naiveAnchor{p: attach(s.toks, x, b)}
	s.syncAnchors()
}

func (s *naiveSim) addRange(id, a, b int, kind RangeKind) {
	sb, eb := endpointBiases(kind)
	s.anchors[id] = naiveAnchor{
		isRange: true,
		kind:    kind,
		s:       attach(s.toks, a, sb),
		e:       attach(s.toks, b, eb),
	}
	s.syncAnchors()
}

func (s *naiveSim) remove(id int) {
	delete(s.anchors, id)
	s.syncAnchors()
}

// fixPoint re-attaches a point whose token was deleted by Replace(p, d, n):
// Left falls back to the survivor just before the span, Right to the one
// just after it.
func fixPoint(pt naivePoint, deleted map[int]bool, old []int, p, d int) naivePoint {
	if !deleted[pt.tok] {
		return pt
	}
	if pt.left {
		if p == 0 {
			return naivePoint{tok: tokStart, left: true}
		}
		return naivePoint{tok: old[p-1], left: true}
	}
	if p+d == len(old) {
		return naivePoint{tok: tokEnd}
	}
	return naivePoint{tok: old[p+d]}
}

func (s *naiveSim) replace(p, d, n int) {
	old := s.toks
	deleted := map[int]bool{}
	for _, tok := range old[p : p+d] {
		deleted[tok] = true
	}
	nt := make([]int, 0, len(old)-d+n)
	nt = append(nt, old[:p]...)
	for i := 0; i < n; i++ {
		nt = append(nt, s.nextTok)
		s.nextTok++
	}
	nt = append(nt, old[p+d:]...)
	s.toks = nt
	for id, a := range s.anchors {
		a.p = fixPoint(a.p, deleted, old, p, d)
		a.s = fixPoint(a.s, deleted, old, p, d)
		a.e = fixPoint(a.e, deleted, old, p, d)
		s.anchors[id] = a
	}
	s.collapseRanges()
	s.snapshot()
}

func (s *naiveSim) move(p, length, q int) {
	qp := q
	if q > p+length {
		qp = q - length
	}
	old := s.toks
	seg := append([]int(nil), old[p:p+length]...)
	rest := make([]int, 0, len(old)-length)
	rest = append(rest, old[:p]...)
	rest = append(rest, old[p+length:]...)
	nt := make([]int, 0, len(old))
	nt = append(nt, rest[:qp]...)
	nt = append(nt, seg...)
	nt = append(nt, rest[qp:]...)
	s.toks = nt
	s.collapseRanges()
	s.snapshot()
}

// collapseRanges mirrors the tracker's per-edit destructive collapse: when
// the mapped start passes the end, the start is re-attached at the end's
// gap (s' = e') with its own bias.
func (s *naiveSim) collapseRanges() {
	for id, a := range s.anchors {
		if !a.isRange {
			continue
		}
		gs := gapOf(s.toks, a.s)
		ge := gapOf(s.toks, a.e)
		if gs > ge {
			sb, _ := endpointBiases(a.kind)
			a.s = attach(s.toks, ge, sb)
			s.anchors[id] = a
		}
	}
}

func (s *naiveSim) pointAt(id, rev int) int {
	st := s.history[rev]
	return gapOf(st.toks, st.anchors[id].p)
}

func (s *naiveSim) rangeAt(id, rev int) (int, int, bool) {
	st := s.history[rev]
	a := st.anchors[id]
	x := gapOf(st.toks, a.s)
	y := gapOf(st.toks, a.e)
	if x > y {
		x = y
	}
	return x, y, x == y
}

// --- Randomized differential driver ---

type anchorMeta struct {
	isRange bool
	created int
	ckpt    int
	removed bool
}

// runTrial executes one randomized trial and returns a log of "input ->
// output (reason)" lines for every operation.
func runTrial(t *testing.T, seed int64, steps int, verbose bool) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	n0 := rng.Intn(25)
	maxLen := n0 + 30 + rng.Intn(20)
	maxAnchors := 4 + rng.Intn(10)
	tr := NewTracker(n0, maxLen, maxAnchors)
	sim := newNaiveSim(n0)
	metas := map[int]*anchorMeta{}
	nextID := 0
	rev, floor := 0, 0
	live := 0
	wantReplayed := 0
	var lines []string

	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		lines = append(lines, line)
		if verbose {
			t.Logf("%s", line)
		}
	}

	checkQuery := func(step int) {
		id := 1 + rng.Intn(nextID+1) // may be unknown
		asRev := rng.Intn(rev + 2)   // may be in the future
		useRange := rng.Intn(2) == 0
		m, known := metas[id]
		var wantErr error
		switch {
		case !known || m.removed:
			wantErr = ErrNoAnchor
		case useRange != m.isRange:
			wantErr = ErrWrongKind
		case asRev > rev:
			wantErr = ErrFuture
		case asRev < m.created:
			wantErr = ErrNotYet
		case asRev < m.ckpt:
			wantErr = ErrCompacted
		}
		if useRange {
			gs, ge, gcol, err := tr.Range(id, asRev)
			if wantErr != nil {
				if !errors.Is(err, wantErr) {
					t.Errorf("Range(%d, %d): got err %v, want %v", id, asRev, err, wantErr)
				}
				logf("query Range(id=%d, asRev=%d) -> err=%v (priority rule)", id, asRev, err)
				return
			}
			if err != nil {
				t.Errorf("Range(%d, %d): unexpected err %v", id, asRev, err)
				return
			}
			ws, we, wcol := sim.rangeAt(id, asRev)
			if gs != ws || ge != we || gcol != wcol {
				t.Errorf("Range(%d, %d) = (%d,%d,%v), naive wants (%d,%d,%v)",
					id, asRev, gs, ge, gcol, ws, we, wcol)
			}
			wantReplayed += asRev - m.ckpt
			logf("query Range(id=%d, asRev=%d) -> (%d,%d,%v) (naive per-char model agrees; replayed += %d)",
				id, asRev, gs, ge, gcol, asRev-m.ckpt)
		} else {
			got, err := tr.Pos(id, asRev)
			if wantErr != nil {
				if !errors.Is(err, wantErr) {
					t.Errorf("Pos(%d, %d): got err %v, want %v", id, asRev, err, wantErr)
				}
				logf("query Pos(id=%d, asRev=%d) -> err=%v (priority rule)", id, asRev, err)
				return
			}
			if err != nil {
				t.Errorf("Pos(%d, %d): unexpected err %v", id, asRev, err)
				return
			}
			want := sim.pointAt(id, asRev)
			if got != want {
				t.Errorf("step %d: Pos(%d, %d) = %d, naive wants %d", step, id, asRev, got, want)
			}
			wantReplayed += asRev - m.ckpt
			logf("query Pos(id=%d, asRev=%d) -> %d (naive per-char model agrees; replayed += %d)",
				id, asRev, got, asRev-m.ckpt)
		}
	}

	for step := 0; step < steps; step++ {
		L := tr.Len()
		switch op := rng.Intn(100); {
		case op < 35: // Replace
			p := rng.Intn(L + 1)
			d := rng.Intn(L - p + 1)
			n := rng.Intn(5)
			if rng.Intn(10) == 0 { // inject a rejected edit
				switch rng.Intn(3) {
				case 0:
					d = L - p + 1 + rng.Intn(3) // p+d > L
				case 1:
					d, n = 0, 0 // d+n < 1
				case 2:
					n = maxLen + 5 // exceeds MaxLen
				}
			}
			gotRev, err := tr.Replace(p, d, n)
			switch {
			case d+n < 1 || p+d > L:
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("Replace(%d,%d,%d): got %v, want ErrInvalid", p, d, n, err)
				}
				logf("step %d: Replace(%d,%d,%d) L=%d -> ErrInvalid (params out of domain)", step, p, d, n, L)
			case L-d+n > maxLen:
				if !errors.Is(err, ErrTooLarge) {
					t.Errorf("Replace(%d,%d,%d): got %v, want ErrTooLarge", p, d, n, err)
				}
				logf("step %d: Replace(%d,%d,%d) L=%d -> ErrTooLarge (%d-%d+%d > MaxLen=%d)", step, p, d, n, L, L, d, n, maxLen)
			default:
				if err != nil || gotRev != rev+1 {
					t.Errorf("Replace(%d,%d,%d): got rev=%d err=%v, want rev=%d", p, d, n, gotRev, err, rev+1)
				}
				rev++
				sim.replace(p, d, n)
				logf("step %d: Replace(%d,%d,%d) -> rev=%d L=%d", step, p, d, n, rev, L-d+n)
			}
		case op < 55: // Move
			if L == 0 {
				continue
			}
			p := rng.Intn(L)
			length := 1 + rng.Intn(L-p)
			q := rng.Intn(L + 1)
			if rng.Intn(10) == 0 {
				q = p + rng.Intn(length+1) // force q into [p, p+len]
			}
			gotRev, err := tr.Move(p, length, q)
			if q >= p && q <= p+length {
				if !errors.Is(err, ErrInvalid) {
					t.Errorf("Move(%d,%d,%d): got %v, want ErrInvalid", p, length, q, err)
				}
				logf("step %d: Move(%d,%d,%d) -> ErrInvalid (q in [p, p+len])", step, p, length, q)
			} else {
				if err != nil || gotRev != rev+1 {
					t.Errorf("Move(%d,%d,%d): got rev=%d err=%v, want rev=%d", p, length, q, gotRev, err, rev+1)
				}
				rev++
				sim.move(p, length, q)
				logf("step %d: Move(%d,%d,%d) -> rev=%d", step, p, length, q, rev)
			}
		case op < 70: // AddPoint
			pos := rng.Intn(L + 2) // may be L+1 -> out of range
			bias := Left
			if rng.Intn(2) == 0 {
				bias = Right
			}
			id, err := tr.AddPoint(pos, bias)
			switch {
			case pos > L:
				if !errors.Is(err, ErrOutOfRange) {
					t.Errorf("AddPoint(%d): got %v, want ErrOutOfRange", pos, err)
				}
				logf("step %d: AddPoint(%d,%v) L=%d -> ErrOutOfRange", step, pos, bias, L)
			case live >= maxAnchors:
				if !errors.Is(err, ErrTooMany) {
					t.Errorf("AddPoint(%d): got %v, want ErrTooMany", pos, err)
				}
				logf("step %d: AddPoint(%d,%v) -> ErrTooMany (cap=%d)", step, pos, bias, maxAnchors)
			default:
				if err != nil {
					t.Errorf("AddPoint(%d): unexpected %v", pos, err)
				}
				nextID++
				if id != nextID {
					t.Errorf("AddPoint id=%d, want %d (monotonic, no reuse)", id, nextID)
				}
				metas[id] = &anchorMeta{created: rev, ckpt: rev}
				sim.addPoint(id, pos, bias)
				live++
				logf("step %d: AddPoint(%d,%v) -> id=%d at rev=%d", step, pos, bias, id, rev)
			}
		case op < 80: // AddRange
			a := rng.Intn(L + 2)
			b := rng.Intn(L + 2)
			kind := Tight
			if rng.Intn(2) == 0 {
				kind = Loose
			}
			id, err := tr.AddRange(a, b, kind)
			switch {
			case a > b || b > L:
				if !errors.Is(err, ErrOutOfRange) {
					t.Errorf("AddRange(%d,%d): got %v, want ErrOutOfRange", a, b, err)
				}
				logf("step %d: AddRange(%d,%d,%v) L=%d -> ErrOutOfRange", step, a, b, kind, L)
			case live >= maxAnchors:
				if !errors.Is(err, ErrTooMany) {
					t.Errorf("AddRange(%d,%d): got %v, want ErrTooMany", a, b, err)
				}
				logf("step %d: AddRange(%d,%d,%v) -> ErrTooMany (cap=%d)", step, a, b, kind, maxAnchors)
			default:
				if err != nil {
					t.Errorf("AddRange(%d,%d): unexpected %v", a, b, err)
				}
				nextID++
				if id != nextID {
					t.Errorf("AddRange id=%d, want %d (monotonic, no reuse)", id, nextID)
				}
				metas[id] = &anchorMeta{isRange: true, created: rev, ckpt: rev}
				sim.addRange(id, a, b, kind)
				live++
				logf("step %d: AddRange(%d,%d,%v) -> id=%d at rev=%d", step, a, b, kind, id, rev)
			}
		case op < 87: // Remove
			if nextID == 0 {
				continue
			}
			id := 1 + rng.Intn(nextID)
			err := tr.Remove(id)
			m, known := metas[id]
			if !known || m.removed {
				if !errors.Is(err, ErrNoAnchor) {
					t.Errorf("Remove(%d): got %v, want ErrNoAnchor", id, err)
				}
				logf("step %d: Remove(%d) -> ErrNoAnchor", step, id)
			} else {
				if err != nil {
					t.Errorf("Remove(%d): unexpected %v", id, err)
				}
				m.removed = true
				live--
				sim.remove(id)
				logf("step %d: Remove(%d) -> ok", step, id)
			}
		case op < 93: // Compact
			newFloor := floor
			if rev > floor {
				newFloor = floor + rng.Intn(rev-floor+1)
			}
			if rng.Intn(10) == 0 {
				newFloor = rev + 1 + rng.Intn(3) // invalid: above rev
			}
			err := tr.Compact(newFloor)
			if newFloor < floor || newFloor > rev {
				if !errors.Is(err, ErrBadFloor) {
					t.Errorf("Compact(%d): got %v, want ErrBadFloor", newFloor, err)
				}
				logf("step %d: Compact(%d) floor=%d rev=%d -> ErrBadFloor", step, newFloor, floor, rev)
			} else {
				if err != nil {
					t.Errorf("Compact(%d): unexpected %v", newFloor, err)
				}
				materialized := 0
				for id, m := range metas {
					if m.removed || m.ckpt >= newFloor {
						continue
					}
					wantReplayed += newFloor - m.ckpt
					m.ckpt = newFloor
					materialized++
					_ = id
				}
				floor = newFloor
				for id, m := range metas {
					if !m.removed && tr.anchors[id].checkpointRev != m.ckpt {
						t.Errorf("anchor %d checkpoint=%d, want %d", id, tr.anchors[id].checkpointRev, m.ckpt)
					}
				}
				logf("step %d: Compact(%d) -> ok, materialized=%d anchors", step, newFloor, materialized)
			}
		}
		checkQuery(step)
		checkQuery(step)
		if tr.replayed != wantReplayed {
			t.Errorf("step %d: replayed=%d, want %d (sum of asRev-checkpoint)", step, tr.replayed, wantReplayed)
		}
		if tr.Rev() != rev || tr.Floor() != floor {
			t.Errorf("step %d: tracker rev/floor = %d/%d, want %d/%d", step, tr.Rev(), tr.Floor(), rev, floor)
		}
	}
	return lines
}

func TestRandomAgainstNaive(t *testing.T) {
	edits := 0
	// 70 trials x ~55% of 80 steps -> well over 2000 random edits.
	for trial := 0; trial < 70; trial++ {
		seed := int64(1000 + trial)
		lines := runTrial(t, seed, 80, trial == 0)
		for _, ln := range lines {
			if strings.Contains(ln, "-> rev=") {
				edits++
			}
		}
		if t.Failed() {
			t.Logf("failing trial seed=%d log:", seed)
			for _, ln := range lines {
				t.Logf("  %s", ln)
			}
			t.FailNow()
		}
	}
	t.Logf("differential test: %d random edits+anchors across 70 trials, all matched naive per-character model", edits)
	if edits < 2000 {
		t.Fatalf("only %d edits applied, want >= 2000", edits)
	}
}

func TestDeterministicReplay(t *testing.T) {
	a := runTrial(t, 424242, 120, false)
	b := runTrial(t, 424242, 120, false)
	if len(a) != len(b) {
		t.Fatalf("log lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("step %d differs:\n  %s\n  %s", i, a[i], b[i])
		}
	}
}
