package mv2pl

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// canonResult is the comparable form of one call's outcome.
type canonResult struct {
	OK       bool
	Reject   RejectReason
	Value    int64
	Deadlock bool
	Events   []Event
}

func canon(res Result) canonResult {
	cr := canonResult{OK: res.OK, Reject: res.Reject, Value: res.Value, Deadlock: res.Deadlock, Events: res.Events}
	if len(cr.Events) == 0 {
		cr.Events = nil
	}
	for i := range cr.Events {
		if len(cr.Events[i].Writes) == 0 {
			cr.Events[i].Writes = nil
		}
	}
	return cr
}

// replayChecker validates the serializability claim: replaying the
// committed transactions in commit-event order must reproduce every
// value any transaction ever read.
type replayChecker struct {
	t         *testing.T
	committed map[int]int64
	buffers   map[int]map[int]int64
}

func newReplayChecker(t *testing.T) *replayChecker {
	return &replayChecker{t: t, committed: map[int]int64{}, buffers: map[int]map[int]int64{}}
}

func (c *replayChecker) setBuffer(txn, key int, val int64) {
	if c.buffers[txn] == nil {
		c.buffers[txn] = map[int]int64{}
	}
	c.buffers[txn][key] = val
}

func (c *replayChecker) observeEvents(seed int64, step int, events []Event) {
	for _, ev := range events {
		switch {
		case ev.Kind == EventGrant && ev.Mode == S:
			if want := c.committed[ev.Key]; ev.Value != want {
				c.t.Fatalf("seed=%d step=%d: %v reads %d, serial replay has committed %d",
					seed, step, ev, ev.Value, want)
			}
		case ev.Kind == EventGrant && ev.Mode == X:
			c.setBuffer(ev.Txn, ev.Key, ev.Value)
		case ev.Kind == EventCommit:
			for _, w := range ev.Writes {
				c.committed[w.Key] = w.Value
			}
			delete(c.buffers, ev.Txn)
		}
	}
}

func hasGrant(res Result, txn int, mode Mode) bool {
	for _, ev := range res.Events {
		if ev.Kind == EventGrant && ev.Txn == txn && ev.Mode == mode {
			return true
		}
	}
	return false
}

func (c *replayChecker) afterWrite(seed int64, step, txn, key int, val int64, res Result) {
	if !res.OK {
		return
	}
	if res.Deadlock {
		delete(c.buffers, txn)
		return
	}
	if !hasGrant(res, txn, X) {
		// no grant event means the txn already held X and overwrote
		c.setBuffer(txn, key, val)
	}
}

func (c *replayChecker) afterRead(seed int64, step, txn, key int, res Result, st State) {
	if !res.OK {
		return
	}
	if res.Deadlock {
		delete(c.buffers, txn)
		return
	}
	if st == Waiting || hasGrant(res, txn, S) {
		return // queued (value comes via grant event) or already checked
	}
	want := c.committed[key]
	if buf, ok := c.buffers[txn][key]; ok {
		want = buf
	}
	if res.Value != want {
		c.t.Fatalf("seed=%d step=%d: read(t%d,k%d) = %d, serial replay expects %d",
			seed, step, txn, key, res.Value, want)
	}
}

func (c *replayChecker) afterFinish(txn int, res Result) {
	if res.OK {
		delete(c.buffers, txn) // committed or aborted: buffer is gone
	}
}

// checkInvariants verifies the structural invariants of the manager
// after every single call.
func checkInvariants(t *testing.T, m *Manager, seed int64, step int) {
	t.Helper()
	where := fmt.Sprintf("seed=%d step=%d", seed, step)
	for key := 0; key < m.keys; key++ {
		gs := m.granted[key]
		for i := 0; i < len(gs); i++ {
			for j := i + 1; j < len(gs); j++ {
				if gs[i].txn != gs[j].txn && !compatible(gs[i].mode, gs[j].mode) {
					t.Fatalf("%s: k%d holds incompatible grants %v and %v", where, key, gs[i], gs[j])
				}
			}
			if m.txns[gs[i].txn].locks[key] != gs[i].mode {
				t.Fatalf("%s: k%d grant %v not reflected in txn locks %v", where, key, gs[i], m.txns[gs[i].txn].locks)
			}
		}
		q := m.queues[key]
		seenOrdinary := false
		for _, r := range q {
			if r.conv {
				if seenOrdinary {
					t.Fatalf("%s: k%d queues a conversion behind an ordinary request: %v", where, key, q)
				}
			} else {
				seenOrdinary = true
			}
			st := m.txns[r.txn].state
			if st != Waiting && st != Committing {
				t.Fatalf("%s: k%d queue entry of t%d in state %s", where, key, r.txn, st)
			}
		}
		if len(q) > 0 && m.grantable(key, q[0].txn, q[0].mode) {
			t.Fatalf("%s: k%d queue head %+v is grantable but still queued", where, key, q[0])
		}
	}
	for id, tx := range m.txns {
		for key := range tx.buffer {
			if tx.locks[key] != X && tx.locks[key] != C {
				t.Fatalf("%s: t%d buffers k%d without holding X/C (%v)", where, id, key, tx.locks)
			}
		}
	}
}

type opKind int

const (
	opBegin opKind = iota
	opRead
	opWrite
	opCommit
	opAbort
)

type op struct {
	kind     opKind
	txn, key int
	val      int64
}

func genOps(rng *rand.Rand, k, steps int) []op {
	ops := make([]op, 0, steps)
	txns := 0
	for i := 0; i < steps; i++ {
		roll := rng.Intn(100)
		pickTxn := func() int {
			if txns > 0 && rng.Intn(100) < 85 {
				return 1 + rng.Intn(txns)
			}
			return txns + 1 + rng.Intn(2) // likely invalid id
		}
		pickKey := func() int {
			if rng.Intn(100) < 85 {
				return rng.Intn(k)
			}
			if rng.Intn(2) == 0 {
				return -1
			}
			return k + rng.Intn(2)
		}
		switch {
		case roll < 20 || txns == 0:
			txns++
			ops = append(ops, op{kind: opBegin})
		case roll < 50:
			ops = append(ops, op{kind: opRead, txn: pickTxn(), key: pickKey()})
		case roll < 75:
			ops = append(ops, op{kind: opWrite, txn: pickTxn(), key: pickKey(), val: int64(rng.Intn(1000))})
		case roll < 90:
			ops = append(ops, op{kind: opCommit, txn: pickTxn()})
		default:
			ops = append(ops, op{kind: opAbort, txn: pickTxn()})
		}
	}
	return ops
}

// runSequence replays the generated ops on a fresh Manager (and, when
// crossCheck is set, on the naive simulator), validating invariants and
// the serial replay after every call. It returns the canonical results.
func runSequence(t *testing.T, seed int64, crossCheck bool) []canonResult {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	k := 1 + rng.Intn(6)
	ops := genOps(rng, k, 25)
	m, err := New(k)
	if err != nil {
		t.Fatalf("New(%d): %v", k, err)
	}
	var n *naive
	if crossCheck {
		n = newNaive(k)
	}
	checker := newReplayChecker(t)
	results := make([]canonResult, 0, len(ops))
	for step, o := range ops {
		var res Result
		var naiveRes Result
		desc := ""
		switch o.kind {
		case opBegin:
			id := m.Begin()
			res = Result{OK: true, Value: int64(id)}
			if crossCheck {
				naiveRes = Result{OK: true, Value: int64(n.begin())}
			}
			desc = fmt.Sprintf("begin() -> t%d", id)
		case opRead:
			res = m.Read(o.txn, o.key)
			if crossCheck {
				naiveRes = n.read(o.txn, o.key)
			}
			desc = fmt.Sprintf("read(t%d,k%d)", o.txn, o.key)
		case opWrite:
			res = m.Write(o.txn, o.key, o.val)
			if crossCheck {
				naiveRes = n.write(o.txn, o.key, o.val)
			}
			desc = fmt.Sprintf("write(t%d,k%d,%d)", o.txn, o.key, o.val)
		case opCommit:
			res = m.Commit(o.txn)
			if crossCheck {
				naiveRes = n.commit(o.txn)
			}
			desc = fmt.Sprintf("commit(t%d)", o.txn)
		case opAbort:
			res = m.Abort(o.txn)
			if crossCheck {
				naiveRes = n.abort(o.txn)
			}
			desc = fmt.Sprintf("abort(t%d)", o.txn)
		}
		got := canon(res)
		results = append(results, got)
		t.Logf("seed=%d step=%d %s -> ok=%v reject=%s value=%d deadlock=%v events=%v",
			seed, step, desc, res.OK, res.Reject, res.Value, res.Deadlock, res.Events)
		if crossCheck {
			want := canon(naiveRes)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d step=%d %s:\n manager = %+v\n naive   = %+v", seed, step, desc, got, want)
			}
		}
		checker.observeEvents(seed, step, res.Events)
		switch o.kind {
		case opRead:
			st := Active
			if tx, ok := m.txns[o.txn]; ok {
				st = tx.state
			}
			checker.afterRead(seed, step, o.txn, o.key, res, st)
		case opWrite:
			checker.afterWrite(seed, step, o.txn, o.key, o.val, res)
		case opCommit, opAbort:
			checker.afterFinish(o.txn, res)
		}
		checkInvariants(t, m, seed, step)
	}
	return results
}

// TestRandomAgainstNaive cross-checks the manager against the naive
// step-by-step simulation for 2000 random call sequences, logging every
// input, output and the divergence reason on mismatch.
func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		runSequence(t, seed, true)
		if t.Failed() {
			t.Fatalf("aborting at seed=%d", seed)
		}
	}
}

// TestReplayDeterminism: the same call sequence replayed on a fresh
// manager must produce identical results and event sequences.
func TestReplayDeterminism(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		first := runSequence(t, seed, false)
		second := runSequence(t, seed, false)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("seed=%d: replay diverged:\n first  = %v\n second = %v", seed, first, second)
		}
	}
}

// TestConcurrentSmoke hammers the manager from many goroutines (run
// with -race); every call is serialized by the manager's lock, and the
// structural invariants must hold once the dust settles.
func TestConcurrentSmoke(t *testing.T) {
	m := mustNew(t, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var mine []int
			for i := 0; i < 300; i++ {
				if len(mine) == 0 || rng.Intn(100) < 15 {
					mine = append(mine, m.Begin())
					continue
				}
				txn := mine[rng.Intn(len(mine))]
				switch rng.Intn(4) {
				case 0:
					m.Read(txn, rng.Intn(8))
				case 1:
					m.Write(txn, rng.Intn(8), int64(rng.Intn(100)))
				case 2:
					m.Commit(txn)
				case 3:
					m.Abort(txn)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	checkInvariants(t, m, -1, -1)
}
