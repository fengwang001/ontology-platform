// A naive reference model, transcribed as literally as possible from
// the specification, plus a randomized differential test that replays
// 2000 operation sequences against the real implementation.
//
// The naive model is deliberately structured differently from the
// production code: its restart precomputes the whole remaining undo
// record sequence in one shot (a pure function of the current log and
// loser set) and then serves Restart/RestartStep by consuming that
// list, whereas the production code steps an incremental, resumable
// state machine. Agreement of the two is strong evidence both match
// the specification.
package aries

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type naive struct {
	lmax    int
	log     []Record
	pages   map[int]int64
	last    map[int]int
	known   map[int]bool
	active  map[int]bool
	crashed bool
	losers  []int
	pending []Record // remaining undo-phase records; nil until computed
}

func newNaive(lmax int) *naive {
	return &naive{
		lmax:   lmax,
		pages:  map[int]int64{},
		last:   map[int]int{},
		known:  map[int]bool{},
		active: map[int]bool{},
	}
}

func (n *naive) append(rec Record) int {
	rec.LSN = len(n.log) + 1
	n.log = append(n.log, rec)
	n.last[rec.Txn] = rec.LSN
	if rec.Type == RecUpdate || rec.Type == RecComp {
		n.pages[rec.Page] += rec.Delta
	}
	return rec.LSN
}

func (n *naive) next(t int) int {
	last := n.last[t]
	if last == 0 {
		return 0
	}
	rec := n.log[last-1]
	if rec.Type == RecComp {
		return rec.UndoNext
	}
	return last
}

// liveTxn applies the shared check order: crashed, not found,
// terminated. Parameter checks run before this.
func (n *naive) liveTxn(t int) error {
	if n.crashed {
		return newError(ErrCrashed, "system is crashed")
	}
	if !n.known[t] {
		return newError(ErrTxnNotFound, "txn %d does not exist", t)
	}
	if !n.active[t] {
		return newError(ErrTxnTerminated, "txn %d is terminated", t)
	}
	return nil
}

func validTxnID(t int) bool { return t >= 1 && t <= 1_000_000 }

func (n *naive) begin(t int) error {
	if !validTxnID(t) {
		return newError(ErrInvalidParam, "bad txn id")
	}
	if n.crashed {
		return newError(ErrCrashed, "system is crashed")
	}
	if n.known[t] {
		return newError(ErrTxnExists, "txn exists")
	}
	n.known[t] = true
	n.active[t] = true
	return nil
}

func (n *naive) update(t, p int, d int64) (int, error) {
	if !validTxnID(t) || p < 0 || p > 1_000_000 || d == 0 || d < -1_000_000_000 || d > 1_000_000_000 {
		return 0, newError(ErrInvalidParam, "bad parameter")
	}
	if err := n.liveTxn(t); err != nil {
		return 0, err
	}
	if len(n.log)+1 > n.lmax {
		return 0, newError(ErrLogFull, "log full")
	}
	rec := Record{Type: RecUpdate, Txn: t, Page: p, Delta: d, Prev: n.last[t]}
	return n.append(rec), nil
}

func (n *naive) save(t int) (int, error) {
	if !validTxnID(t) {
		return 0, newError(ErrInvalidParam, "bad txn id")
	}
	if err := n.liveTxn(t); err != nil {
		return 0, err
	}
	return n.last[t], nil
}

func (n *naive) commit(t int) error {
	if !validTxnID(t) {
		return newError(ErrInvalidParam, "bad txn id")
	}
	if err := n.liveTxn(t); err != nil {
		return err
	}
	if len(n.log)+1 > n.lmax {
		return newError(ErrLogFull, "log full")
	}
	n.append(Record{Type: RecCommit, Txn: t, Prev: n.last[t]})
	n.active[t] = false
	return nil
}

// undoList walks from q down to savepoint sp and returns the U records
// that would be undone, in undo order. Pure: it mutates nothing.
func (n *naive) undoList(q, sp int) []Record {
	var out []Record
	for q > sp {
		rec := n.log[q-1]
		if rec.Type == RecUpdate {
			out = append(out, rec)
			q = rec.Prev
		} else {
			q = rec.UndoNext
		}
	}
	return out
}

func (n *naive) validSavepoint(t, sp int) bool {
	if sp == 0 {
		return true
	}
	if sp < 0 || sp > len(n.log) {
		return false
	}
	return n.log[sp-1].Txn == t
}

func (n *naive) applyUndo(t int, us []Record) {
	for _, u := range us {
		n.append(Record{
			Type:     RecComp,
			Txn:      t,
			Page:     u.Page,
			Delta:    -u.Delta,
			Prev:     n.last[t],
			UndoNext: u.Prev,
		})
	}
}

func (n *naive) rollback(t, sp int) (int, error) {
	if !validTxnID(t) {
		return 0, newError(ErrInvalidParam, "bad txn id")
	}
	if err := n.liveTxn(t); err != nil {
		return 0, err
	}
	if !n.validSavepoint(t, sp) {
		return 0, newError(ErrInvalidSavepoint, "bad savepoint")
	}
	us := n.undoList(n.next(t), sp)
	if len(n.log)+len(us) > n.lmax {
		return 0, newError(ErrLogFull, "log full")
	}
	n.applyUndo(t, us)
	return len(us), nil
}

func (n *naive) abort(t int) (int, error) {
	if !validTxnID(t) {
		return 0, newError(ErrInvalidParam, "bad txn id")
	}
	if err := n.liveTxn(t); err != nil {
		return 0, err
	}
	us := n.undoList(n.next(t), 0)
	if len(n.log)+len(us)+1 > n.lmax {
		return 0, newError(ErrLogFull, "log full")
	}
	n.applyUndo(t, us)
	n.append(Record{Type: RecEnd, Txn: t, Prev: n.last[t]})
	n.active[t] = false
	return len(us) + 1, nil
}

func (n *naive) crash() {
	if n.crashed {
		return
	}
	n.crashed = true
	n.pending = nil
	n.losers = n.losers[:0]
	for id := range n.known {
		if n.active[id] {
			n.losers = append(n.losers, id)
		}
	}
	sort.Ints(n.losers)
}

// genUndo computes the full remaining undo-phase record sequence in
// one shot, exactly per the specification: first an E for every loser
// whose next is already 0 (ascending txn id), then repeatedly process
// the loser with the largest next, emitting an E the moment any
// loser's next becomes 0.
func (n *naive) genUndo() []Record {
	next := map[int]int{}
	last := map[int]int{}
	ended := map[int]bool{}
	for _, id := range n.losers {
		next[id] = n.next(id)
		last[id] = n.last[id]
	}
	var out []Record
	lsn := len(n.log)
	emit := func(rec Record) {
		lsn++
		rec.LSN = lsn
		out = append(out, rec)
		last[rec.Txn] = lsn
	}
	for _, id := range n.losers {
		if next[id] == 0 {
			emit(Record{Type: RecEnd, Txn: id, Prev: last[id]})
			ended[id] = true
		}
	}
	for {
		best, bestNext := -1, 0
		for _, id := range n.losers {
			if !ended[id] && next[id] > bestNext {
				best, bestNext = id, next[id]
			}
		}
		if best == -1 {
			break
		}
		rec := n.log[bestNext-1] // only ever indexes the original log
		if rec.Type == RecUpdate {
			emit(Record{
				Type:     RecComp,
				Txn:      best,
				Page:     rec.Page,
				Delta:    -rec.Delta,
				Prev:     last[best],
				UndoNext: rec.Prev,
			})
			next[best] = rec.Prev
		} else {
			next[best] = rec.UndoNext
		}
		if next[best] == 0 {
			emit(Record{Type: RecEnd, Txn: best, Prev: last[best]})
			ended[best] = true
		}
	}
	return out
}

// consume applies up to m pending undo records (all of them if m < 0)
// and finishes the restart when the pending list is exhausted.
func (n *naive) consume(m int) int {
	if n.pending == nil {
		n.pending = n.genUndo()
	}
	k := m
	if k < 0 || k > len(n.pending) {
		k = len(n.pending)
	}
	for i := 0; i < k; i++ {
		n.append(n.pending[i])
	}
	n.pending = n.pending[k:]
	if len(n.pending) == 0 {
		for _, id := range n.losers {
			n.active[id] = false
		}
		n.losers = nil
		n.crashed = false
	}
	return k
}

func (n *naive) restart() (int, error) {
	if !n.crashed {
		return 0, newError(ErrNotCrashed, "not crashed")
	}
	return n.consume(-1), nil
}

func (n *naive) restartStep(m int) (int, error) {
	if m < 1 {
		return 0, newError(ErrInvalidParam, "bad n")
	}
	if !n.crashed {
		return 0, newError(ErrNotCrashed, "not crashed")
	}
	return n.consume(m), nil
}

// --- Differential fuzz test ---

func errCode(err error) int {
	if err == nil {
		return -1
	}
	var ae *Error
	if errors.As(err, &ae) {
		return int(ae.Code)
	}
	return -2
}

type fuzzOp struct {
	desc string
	run  func() (int, error)
}

// TestDifferentialFuzz replays 2000 random operation sequences against
// both the real System and the naive model, comparing every return
// value and error code, and the full log and page values at the end.
func TestDifferentialFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		runFuzzSequence(t, rng, seq)
	}
}

func runFuzzSequence(t *testing.T, rng *rand.Rand, seq int) {
	t.Helper()
	lmax := 1 + rng.Intn(60)
	sys, err := NewSystem(lmax)
	if err != nil {
		t.Fatalf("seq %d: NewSystem: %v", seq, err)
	}
	nv := newNaive(lmax)

	txnPool := 1 + rng.Intn(5)
	pagePool := 1 + rng.Intn(4)
	numOps := 10 + rng.Intn(40)

	var trace []string
	fail := func(format string, args ...any) {
		t.Helper()
		t.Logf("seq %d FAILED: %s", seq, fmt.Sprintf(format, args...))
		for i, line := range trace {
			t.Logf("  op %d: %s", i, line)
		}
		t.FailNow()
	}

	// Every op is recorded so the whole sequence can be replayed on a
	// fresh System to verify deterministic reproduction.
	type recordedOp struct {
		desc  string
		apply func(s *System) (int, error)
	}
	var recorded []recordedOp

	randTxn := func() int {
		switch rng.Intn(10) {
		case 0:
			return 0 // invalid
		case 1:
			return 1_000_001 // invalid
		default:
			return 1 + rng.Intn(txnPool)
		}
	}
	randPage := func() int {
		if rng.Intn(12) == 0 {
			return -1 // invalid
		}
		return rng.Intn(pagePool)
	}
	randDelta := func() int64 {
		switch rng.Intn(12) {
		case 0:
			return 0 // invalid
		case 1:
			return 2_000_000_000 // invalid
		default:
			d := int64(1 + rng.Intn(100))
			if rng.Intn(2) == 0 {
				d = -d
			}
			return d
		}
	}
	randSavepoint := func() int {
		logLen := len(sys.Log())
		switch rng.Intn(4) {
		case 0:
			return 0
		case 1:
			return rng.Intn(logLen + 3) // may be invalid or another txn's
		default:
			if logLen == 0 {
				return 0
			}
			return 1 + rng.Intn(logLen)
		}
	}

	for i := 0; i < numOps; i++ {
		var gotR, gotN int
		var errR, errN error
		var desc string
		var apply func(s *System) (int, error)
		switch rng.Intn(12) {
		case 0, 1:
			id := randTxn()
			desc = fmt.Sprintf("Begin(%d)", id)
			apply = func(s *System) (int, error) { return 0, s.Begin(id) }
			gotR, errR = apply(sys)
			errN = nv.begin(id)
		case 2, 3, 4, 5:
			id, p, d := randTxn(), randPage(), randDelta()
			desc = fmt.Sprintf("Update(%d,%d,%d)", id, p, d)
			apply = func(s *System) (int, error) { return s.Update(id, p, d) }
			gotR, errR = apply(sys)
			gotN, errN = nv.update(id, p, d)
		case 6:
			id := randTxn()
			desc = fmt.Sprintf("Save(%d)", id)
			apply = func(s *System) (int, error) { return s.Save(id) }
			gotR, errR = apply(sys)
			gotN, errN = nv.save(id)
		case 7:
			id, sp := randTxn(), randSavepoint()
			desc = fmt.Sprintf("Rollback(%d,%d)", id, sp)
			apply = func(s *System) (int, error) { return s.Rollback(id, sp) }
			gotR, errR = apply(sys)
			gotN, errN = nv.rollback(id, sp)
		case 8:
			id := randTxn()
			desc = fmt.Sprintf("Abort(%d)", id)
			apply = func(s *System) (int, error) { return s.Abort(id) }
			gotR, errR = apply(sys)
			gotN, errN = nv.abort(id)
		case 9:
			id := randTxn()
			desc = fmt.Sprintf("Commit(%d)", id)
			apply = func(s *System) (int, error) { return 0, s.Commit(id) }
			gotR, errR = apply(sys)
			errN = nv.commit(id)
		case 10:
			switch rng.Intn(3) {
			case 0:
				desc = "Crash()"
				apply = func(s *System) (int, error) { s.Crash(); return 0, nil }
				gotR, errR = apply(sys)
				nv.crash()
			case 1:
				desc = "Restart()"
				apply = func(s *System) (int, error) { return s.Restart() }
				gotR, errR = apply(sys)
				gotN, errN = nv.restart()
			case 2:
				m := 1 + rng.Intn(4)
				if rng.Intn(10) == 0 {
					m = 0 // invalid
				}
				desc = fmt.Sprintf("RestartStep(%d)", m)
				apply = func(s *System) (int, error) { return s.RestartStep(m) }
				gotR, errR = apply(sys)
				gotN, errN = nv.restartStep(m)
			}
		case 11:
			id := randTxn()
			desc = fmt.Sprintf("Save(%d) [dup-check]", id)
			apply = func(s *System) (int, error) { return s.Save(id) }
			gotR, errR = apply(sys)
			gotN, errN = nv.save(id)
		}
		recorded = append(recorded, recordedOp{desc: desc, apply: apply})

		codeR, codeN := errCode(errR), errCode(errN)
		judgment := fmt.Sprintf("real=(ret=%d,code=%d) naive=(ret=%d,code=%d)",
			gotR, codeR, gotN, codeN)
		trace = append(trace, desc+" -> "+judgment)
		if codeR != codeN {
			fail("error code mismatch on %s: %s", desc, judgment)
		}
		if codeR == -1 && gotR != gotN {
			fail("return value mismatch on %s: %s", desc, judgment)
		}
	}

	// Final state comparison: full log, all pages, crashed flag.
	logR, logN := sys.Log(), nv.log
	if !reflect.DeepEqual(append([]Record{}, logR...), append([]Record{}, logN...)) {
		fail("final log mismatch:\nreal:  %+v\nnaive: %+v", logR, logN)
	}
	for p := 0; p <= pagePool; p++ {
		if sys.Page(p) != nv.pages[p] {
			fail("page %d mismatch: real=%d naive=%d", p, sys.Page(p), nv.pages[p])
		}
	}
	if sys.Crashed() != nv.crashed {
		fail("crashed flag mismatch: real=%v naive=%v", sys.Crashed(), nv.crashed)
	}

	// Structural invariants on the real log.
	checkStructuralInvariants(t, sys, fail)

	// Replaying the identical operation sequence on a fresh system
	// must reproduce the exact same log and page values.
	sys2, err := NewSystem(lmax)
	if err != nil {
		fail("replay NewSystem: %v", err)
	}
	for _, op := range recorded {
		if _, err := op.apply(sys2); err != nil {
			_ = err // rejections are fine; only the final state matters
		}
	}
	if !reflect.DeepEqual(sys.Log(), sys2.Log()) {
		fail("replay produced a different log")
	}
	for p := 0; p <= pagePool; p++ {
		if sys.Page(p) != sys2.Page(p) {
			fail("replay page %d differs: %d vs %d", p, sys.Page(p), sys2.Page(p))
		}
	}

	// Log the input sequence, outputs and the judgment basis.
	var b strings.Builder
	fmt.Fprintf(&b, "seq %d OK: lmax=%d txns=1..%d pages=0..%d ops=%d;",
		seq, lmax, txnPool, pagePool-1, numOps)
	fmt.Fprintf(&b, " final: logLen=%d crashed=%v; judgment: every op's (ret,errCode) equal, logs/pages identical",
		len(logR), sys.Crashed())
	t.Log(b.String())
	for i, line := range trace {
		t.Logf("  op %d: %s", i, line)
	}
}

// checkStructuralInvariants verifies, on the real system's log:
//   - LSNs are sequential from 1;
//   - every record's prevLSN is the previous LSN of its own txn;
//   - every C has undoNext < its own LSN;
//   - every page value equals the sum of U/C deltas on that page;
//   - per (txn, page, |delta|), no more C records than U records
//     (each U is undone by at most one C).
func checkStructuralInvariants(t *testing.T, s *System, fail func(string, ...any)) {
	t.Helper()
	log := s.Log()
	lastOf := map[int]int{}
	pageSum := map[int]int64{}
	type key struct {
		txn, page int
		mag       int64
	}
	uCount := map[key]int{}
	cCount := map[key]int{}
	for i, r := range log {
		if r.LSN != i+1 {
			fail("record %d has LSN %d", i, r.LSN)
		}
		if r.Prev != lastOf[r.Txn] {
			fail("record %d of txn %d has prevLSN %d, want %d",
				r.LSN, r.Txn, r.Prev, lastOf[r.Txn])
		}
		lastOf[r.Txn] = r.LSN
		if r.Type == RecComp && r.UndoNext >= r.LSN {
			fail("C %d has undoNext %d >= its LSN", r.LSN, r.UndoNext)
		}
		if r.Type == RecUpdate || r.Type == RecComp {
			pageSum[r.Page] += r.Delta
			mag := r.Delta
			if mag < 0 {
				mag = -mag
			}
			if r.Type == RecUpdate {
				uCount[key{r.Txn, r.Page, mag}]++
			} else {
				cCount[key{r.Txn, r.Page, mag}]++
			}
		}
	}
	for p, sum := range pageSum {
		if got := s.Page(p); got != sum {
			fail("page %d = %d, but log deltas sum to %d", p, got, sum)
		}
	}
	for k, c := range cCount {
		if u := uCount[k]; c > u {
			fail("txn %d page %d |delta|=%d: %d C records but only %d U records",
				k.txn, k.page, k.mag, c, u)
		}
	}
}
