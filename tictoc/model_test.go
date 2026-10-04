package tictoc

// A naive, literal step-by-step simulation of the specification, used
// as an independent oracle for randomized differential testing. It is
// deliberately structured differently from Validator (whole-array
// snapshot rollback, plain 0..K-1 scans) so that the two
// implementations only share the spec, not the code.

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

type modelRead struct {
	value int64
	w0    int64
	r0    int64
}

type modelTxn struct {
	state  State
	reads  map[int]modelRead
	writes map[int]int64
	c      int64
}

type model struct {
	k    int
	val  []int64
	w    []int64
	r    []int64
	lock []int
	txns map[int]*modelTxn
	next int
}

func newModel(k int) *model {
	return &model{
		k:    k,
		val:  make([]int64, k),
		w:    make([]int64, k),
		r:    make([]int64, k),
		lock: make([]int, k),
		txns: make(map[int]*modelTxn),
		next: 1,
	}
}

func (m *model) begin() int {
	id := m.next
	m.next++
	m.txns[id] = &modelTxn{
		state:  Active,
		reads:  make(map[int]modelRead),
		writes: make(map[int]int64),
	}
	return id
}

func (m *model) lookup(t int) (*modelTxn, error) {
	tx, ok := m.txns[t]
	if !ok {
		return nil, fmt.Errorf("%w: txn %d", ErrTxnNotFound, t)
	}
	return tx, nil
}

func (m *model) read(t, k int) (int64, error) {
	tx, err := m.lookup(t)
	if err != nil {
		return 0, err
	}
	if tx.state != Active {
		return 0, fmt.Errorf("%w: read", ErrInvalidState)
	}
	if k < 0 || k >= m.k {
		return 0, fmt.Errorf("%w: %d", ErrTupleOutOfRange, k)
	}
	if x, ok := tx.writes[k]; ok {
		return x, nil
	}
	if rec, ok := tx.reads[k]; ok {
		return rec.value, nil
	}
	tx.reads[k] = modelRead{value: m.val[k], w0: m.w[k], r0: m.r[k]}
	return m.val[k], nil
}

func (m *model) write(t, k int, x int64) error {
	tx, err := m.lookup(t)
	if err != nil {
		return err
	}
	if tx.state != Active {
		return fmt.Errorf("%w: write", ErrInvalidState)
	}
	if k < 0 || k >= m.k {
		return fmt.Errorf("%w: %d", ErrTupleOutOfRange, k)
	}
	tx.writes[k] = x
	return nil
}

func (m *model) prepare(t int) (int64, error) {
	tx, err := m.lookup(t)
	if err != nil {
		return 0, err
	}
	if tx.state != Active {
		return 0, fmt.Errorf("%w: prepare", ErrInvalidState)
	}
	// Whole-state snapshot; any abort restores it verbatim.
	val0 := append([]int64(nil), m.val...)
	w0 := append([]int64(nil), m.w...)
	r0 := append([]int64(nil), m.r...)
	lock0 := append([]int(nil), m.lock...)
	rollback := func() {
		copy(m.val, val0)
		copy(m.w, w0)
		copy(m.r, r0)
		copy(m.lock, lock0)
		tx.state = Aborted
	}

	// Step 1: lock the write set in ascending tuple order.
	for k := 0; k < m.k; k++ {
		if _, ok := tx.writes[k]; !ok {
			continue
		}
		if m.lock[k] != 0 && m.lock[k] != t {
			holder := m.lock[k]
			rollback()
			return 0, fmt.Errorf("%w: tuple %d held by %d", ErrAbortLockConflict, k, holder)
		}
		m.lock[k] = t
	}

	// Step 2: commit timestamp.
	var c int64
	if len(tx.writes) > 0 || len(tx.reads) > 0 {
		first := true
		for k := range tx.writes {
			if cand := m.r[k] + 1; first || cand > c {
				c, first = cand, false
			}
		}
		for _, rec := range tx.reads {
			if first || rec.w0 > c {
				c, first = rec.w0, false
			}
		}
	}

	// Step 3: validate the read set in ascending tuple order.
	for k := 0; k < m.k; k++ {
		rec, ok := tx.reads[k]
		if !ok || rec.r0 >= c {
			continue
		}
		if m.w[k] != rec.w0 {
			rollback()
			return 0, fmt.Errorf("%w: tuple %d", ErrAbortVersionChanged, k)
		}
		if m.r[k] >= c {
			continue
		}
		if m.lock[k] != 0 && m.lock[k] != t {
			holder := m.lock[k]
			rollback()
			return 0, fmt.Errorf("%w: tuple %d held by %d", ErrAbortExtendBlocked, k, holder)
		}
		if m.lock[k] == t {
			continue
		}
		m.r[k] = c
	}

	// Step 4: success.
	tx.state = Prepared
	tx.c = c
	return c, nil
}

func (m *model) finish(t int) (int64, error) {
	tx, err := m.lookup(t)
	if err != nil {
		return 0, err
	}
	if tx.state != Prepared {
		return 0, fmt.Errorf("%w: finish", ErrInvalidState)
	}
	for k, x := range tx.writes {
		m.val[k] = x
		m.w[k] = tx.c
		m.r[k] = tx.c
		m.lock[k] = 0
	}
	tx.state = Committed
	return tx.c, nil
}

func (m *model) abort(t int) error {
	tx, err := m.lookup(t)
	if err != nil {
		return err
	}
	if tx.state != Active && tx.state != Prepared {
		return fmt.Errorf("%w: abort", ErrInvalidState)
	}
	for k := range tx.writes {
		if m.lock[k] == t {
			m.lock[k] = 0
		}
	}
	tx.state = Aborted
	return nil
}

// errKind maps an error to a stable label so the two implementations
// can be compared by rejection/abort reason.
func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrTxnNotFound):
		return "txn-not-found"
	case errors.Is(err, ErrInvalidState):
		return "invalid-state"
	case errors.Is(err, ErrTupleOutOfRange):
		return "tuple-out-of-range"
	case errors.Is(err, ErrAbortLockConflict):
		return "abort-lock-conflict"
	case errors.Is(err, ErrAbortVersionChanged):
		return "abort-version-changed"
	case errors.Is(err, ErrAbortExtendBlocked):
		return "abort-extend-blocked"
	}
	return "unknown"
}

// checkInvariants verifies the structural invariants that must hold
// after every call: w <= r, at most one lock holder per tuple, and
// locked tuples are exactly the write sets of prepared transactions.
func checkInvariants(t *testing.T, v *Validator, ctx string) {
	t.Helper()
	v.mu.Lock()
	defer v.mu.Unlock()
	for k, tp := range v.tuples {
		if tp.W > tp.R {
			t.Fatalf("%s: tuple%d violates w<=r: %+v", ctx, k, tp)
		}
		if tp.Lock != 0 {
			tx, ok := v.txns[tp.Lock]
			if !ok || tx.state != Prepared {
				t.Fatalf("%s: tuple%d locked by non-prepared txn %d", ctx, k, tp.Lock)
			}
			if _, ok := tx.writes[k]; !ok {
				t.Fatalf("%s: tuple%d locked by txn %d not in its write set", ctx, k, tp.Lock)
			}
		}
	}
	for id, tx := range v.txns {
		if tx.state != Prepared {
			continue
		}
		for k := range tx.writes {
			if v.tuples[k].Lock != id {
				t.Fatalf("%s: prepared txn %d does not hold lock on write-set tuple %d", ctx, id, k)
			}
		}
	}
}

// committedTxn records what the harness observed for one committed
// transaction, for the serial-replay check.
type committedTxn struct {
	c         int64
	finishSeq int
	reads     map[int]int64 // tuple reads only (no buffer hits)
	writes    map[int]int64
}

// replayCommitted re-executes all committed transactions ordered by
// (commit ts, finish order) and checks that every tuple read returns
// the replayed value.
func replayCommitted(t *testing.T, k int, committed []committedTxn, ctx string) {
	t.Helper()
	sort.SliceStable(committed, func(i, j int) bool {
		if committed[i].c != committed[j].c {
			return committed[i].c < committed[j].c
		}
		return committed[i].finishSeq < committed[j].finishSeq
	})
	mem := make([]int64, k)
	for _, txn := range committed {
		keys := make([]int, 0, len(txn.reads))
		for key := range txn.reads {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		for _, key := range keys {
			if mem[key] != txn.reads[key] {
				t.Fatalf("%s: replay mismatch at (c=%d, seq=%d): tuple %d read %d, replayed %d",
					ctx, txn.c, txn.finishSeq, key, txn.reads[key], mem[key])
			}
		}
		for key, x := range txn.writes {
			mem[key] = x
		}
	}
}

// TestRandomSequencesMatchModel replays 2000 random call sequences
// against both the Validator and the naive model, comparing every
// return value and error reason, the full tuple/transaction state,
// the structural invariants after every call, the Prepare tuple-touch
// bound, and the serial-replay property of committed transactions.
// Each call's input, output and verdict are logged (go test -v).
func TestRandomSequencesMatchModel(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261004))
	abortTally := map[string]int{}
	for seq := 0; seq < sequences; seq++ {
		k := 1 + rng.Intn(8)
		v, err := New(k)
		if err != nil {
			t.Fatalf("seq %d: New(%d): %v", seq, k, err)
		}
		m := newModel(k)
		n := 10 + rng.Intn(40)
		live := []int{}
		var committed []committedTxn
		// Per-txn shadow state to classify reads for the replay check.
		shadowWrites := map[int]map[int]int64{}
		shadowReads := map[int]map[int]int64{}
		finishSeq := 0

		ctx := fmt.Sprintf("seq=%d K=%d", seq, k)
		t.Logf("%s ops=%d", ctx, n)

		pickTxn := func() int {
			if len(live) == 0 || rng.Intn(10) == 0 {
				// Unknown id: 0, negative, or beyond the next id.
				switch rng.Intn(3) {
				case 0:
					return 0
				case 1:
					return -1 - rng.Intn(5)
				default:
					return len(live) + 2 + rng.Intn(5)
				}
			}
			return live[rng.Intn(len(live))]
		}
		pickK := func() int {
			if rng.Intn(10) == 0 {
				if rng.Intn(2) == 0 {
					return -1
				}
				return k + rng.Intn(3)
			}
			return rng.Intn(k)
		}

		for i := 0; i < n; i++ {
			var desc, gotOut, wantOut string
			switch rng.Intn(14) {
			case 0, 1, 2: // Begin
				gotID := v.Begin()
				wantID := m.begin()
				live = append(live, gotID)
				shadowWrites[gotID] = map[int]int64{}
				shadowReads[gotID] = map[int]int64{}
				desc = "Begin()"
				gotOut = fmt.Sprintf("txn=%d", gotID)
				wantOut = fmt.Sprintf("txn=%d", wantID)
			case 3, 4, 5, 6, 7: // Read
				txn, key := pickTxn(), pickK()
				gotVal, gotErr := v.Read(txn, key)
				wantVal, wantErr := m.read(txn, key)
				if gotErr == nil {
					if _, written := shadowWrites[txn][key]; !written {
						if _, seen := shadowReads[txn][key]; !seen {
							shadowReads[txn][key] = gotVal
						}
					}
				}
				desc = fmt.Sprintf("Read(%d,%d)", txn, key)
				gotOut = fmt.Sprintf("(%d,%s)", gotVal, errKind(gotErr))
				wantOut = fmt.Sprintf("(%d,%s)", wantVal, errKind(wantErr))
			case 8, 9, 10: // Write
				txn, key, x := pickTxn(), pickK(), int64(rng.Intn(201)-100)
				gotErr := v.Write(txn, key, x)
				wantErr := m.write(txn, key, x)
				if gotErr == nil {
					shadowWrites[txn][key] = x
				}
				desc = fmt.Sprintf("Write(%d,%d,%d)", txn, key, x)
				gotOut = errKind(gotErr)
				wantOut = errKind(wantErr)
			case 11, 12: // Prepare
				txn := pickTxn()
				gotC, gotErr := v.Prepare(txn)
				wantC, wantErr := m.prepare(txn)
				if tx, ok := v.txns[txn]; ok {
					if bound := 2 * (len(tx.reads) + len(tx.writes)); v.prepareTouches > bound {
						t.Fatalf("%s op %d: Prepare touched %d tuples, bound %d",
							ctx, i, v.prepareTouches, bound)
					}
				}
				desc = fmt.Sprintf("Prepare(%d)", txn)
				gotOut = fmt.Sprintf("(c=%d,%s)", gotC, errKind(gotErr))
				wantOut = fmt.Sprintf("(c=%d,%s)", wantC, errKind(wantErr))
			case 13: // Finish or Abort
				txn := pickTxn()
				if rng.Intn(2) == 0 {
					gotC, gotErr := v.Finish(txn)
					wantC, wantErr := m.finish(txn)
					if gotErr == nil {
						committed = append(committed, committedTxn{
							c:         gotC,
							finishSeq: finishSeq,
							reads:     shadowReads[txn],
							writes:    shadowWrites[txn],
						})
						finishSeq++
					}
					desc = fmt.Sprintf("Finish(%d)", txn)
					gotOut = fmt.Sprintf("(c=%d,%s)", gotC, errKind(gotErr))
					wantOut = fmt.Sprintf("(c=%d,%s)", wantC, errKind(wantErr))
				} else {
					gotErr := v.Abort(txn)
					wantErr := m.abort(txn)
					desc = fmt.Sprintf("Abort(%d)", txn)
					gotOut = errKind(gotErr)
					wantOut = errKind(wantErr)
				}
			}
			// Verdict: outputs (value + reason) must be identical.
			if gotOut != wantOut {
				t.Fatalf("%s op %d\n  input:  %s\n  output: %s\n  oracle: %s\n  verdict: mismatch",
					ctx, i, desc, gotOut, wantOut)
			}
			if gotErr := gotOut; true {
				for _, kind := range []string{"abort-lock-conflict", "abort-version-changed", "abort-extend-blocked"} {
					if strings.Contains(gotErr, kind) {
						abortTally[kind]++
					}
				}
			}
			t.Logf("  op %d: %-24s -> %s [match]", i, desc, gotOut)
			checkInvariants(t, v, fmt.Sprintf("%s op %d (%s)", ctx, i, desc))
		}

		// Final tuple state must be identical, field by field.
		gotTuples := tuplesOf(v)
		wantTuples := make([]Tuple, k)
		for key := range wantTuples {
			wantTuples[key] = Tuple{Value: m.val[key], W: m.w[key], R: m.r[key], Lock: m.lock[key]}
		}
		if !reflect.DeepEqual(gotTuples, wantTuples) {
			t.Fatalf("%s final tuples\n  got:  %v\n  want: %v", ctx, gotTuples, wantTuples)
		}
		// Final transaction states must be identical.
		_, states := v.Snapshot()
		for id, tx := range m.txns {
			if states[id] != tx.state {
				t.Fatalf("%s txn %d state = %s, want %s", ctx, id, states[id], tx.state)
			}
		}
		replayCommitted(t, k, committed, ctx)
		t.Logf("%s final tuples=%v committed=%d [state match, replay ok]",
			ctx, gotTuples, len(committed))
	}
	for _, kind := range []string{"abort-lock-conflict", "abort-version-changed", "abort-extend-blocked"} {
		if abortTally[kind] == 0 {
			t.Fatalf("random run never exercised %s", kind)
		}
	}
	t.Logf("abort reasons exercised: %v", abortTally)
}

// TestConcurrentSmoke hammers one validator from many goroutines;
// with -race it proves data-race freedom, and the invariants must
// still hold afterwards (every call is atomic, so the run is
// equivalent to some serial order).
func TestConcurrentSmoke(t *testing.T) {
	v := mustNew(t, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				txn := v.Begin()
				key := rng.Intn(8)
				if _, err := v.Read(txn, key); err != nil {
					t.Errorf("Read: %v", err)
				}
				if err := v.Write(txn, rng.Intn(8), int64(rng.Intn(100))); err != nil {
					t.Errorf("Write: %v", err)
				}
				if _, err := v.Prepare(txn); err == nil {
					if _, err := v.Finish(txn); err != nil {
						t.Errorf("Finish: %v", err)
					}
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	checkInvariants(t, v, "after concurrent smoke")
}
