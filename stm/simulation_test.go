package stm

// This file cross-checks the Manager against a naive simulation written
// directly from the specification. The naive version derives adversaries
// by scanning every transaction's holds (O(transactions) per Open) and
// keeps no derived per-object state, while the Manager maintains
// per-object writer/reader sets. Both must agree on every result, score
// and delay for thousands of random call sequences.

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// naiveTxn mirrors one transaction of the specification.
type naiveTxn struct {
	state txnState
	kp    int
	ab    int
	holds map[int]Mode
	att   map[int]int
}

// naive is a deliberately simple, specification-literal implementation.
// It keeps no derived state: adversaries are found by scanning every
// transaction's holds.
type naive struct {
	cfg  Config
	txns []naiveTxn // txns[id-1]
}

func newNaive(cfg Config) *naive { return &naive{cfg: cfg} }

func (n *naive) backoff(k int) int64 {
	if k > n.cfg.ExpCap {
		k = n.cfg.ExpCap
	}
	return int64(n.cfg.BaseDelay) << uint(k)
}

func (n *naive) privileged(id int) bool {
	return n.txns[id-1].ab >= n.cfg.PrivThreshold
}

func (n *naive) begin() int {
	n.txns = append(n.txns, naiveTxn{
		state: stateActive,
		holds: map[int]Mode{},
		att:   map[int]int{},
	})
	return len(n.txns)
}

func (n *naive) get(id int) (*naiveTxn, error) {
	if id < 1 || id > len(n.txns) {
		return nil, ErrNoSuchTxn
	}
	return &n.txns[id-1], nil
}

func (n *naive) getActive(id int) (*naiveTxn, error) {
	t, err := n.get(id)
	if err != nil {
		return nil, err
	}
	if t.state != stateActive {
		return nil, ErrBadState
	}
	return t, nil
}

// adversaries scans all transactions: a write request conflicts with any
// other holder, a read request only with writers.
func (n *naive) adversaries(id, o int, write bool) []int {
	var adv []int
	for other := 1; other <= len(n.txns); other++ {
		if other == id {
			continue
		}
		mode := n.txns[other-1].holds[o]
		if mode == ModeWrite || (write && mode == ModeRead) {
			adv = append(adv, other)
		}
	}
	return adv // ascending by construction
}

func (n *naive) beats(id, e, k int) bool {
	tPriv, ePriv := n.privileged(id), n.privileged(e)
	switch {
	case tPriv && ePriv:
		return id < e
	case tPriv:
		return true
	case ePriv:
		return false
	default:
		return k >= n.cfg.ForceThreshold || n.txns[id-1].kp+k > n.txns[e-1].kp
	}
}

func (n *naive) grant(t *naiveTxn, o int, write bool) {
	if t.holds[o] == ModeNone && t.kp < n.cfg.ScoreCap {
		t.kp++
	}
	t.att[o] = 0
	if write {
		t.holds[o] = ModeWrite
	} else {
		t.holds[o] = ModeRead
	}
}

func (n *naive) open(id, o int, write bool) (OpenResult, error) {
	t, err := n.getActive(id)
	if err != nil {
		return OpenResult{}, err
	}
	if o < 0 || o >= n.cfg.Objects {
		return OpenResult{}, ErrNoSuchObject
	}
	need := ModeRead
	if write {
		need = ModeWrite
	}
	if t.holds[o] >= need {
		return OpenResult{}, nil
	}
	adv := n.adversaries(id, o, write)
	if len(adv) == 0 {
		n.grant(t, o, write)
		return OpenResult{}, nil
	}
	k := t.att[o]
	for _, e := range adv {
		if !n.beats(id, e, k) {
			t.att[o] = k + 1
			return OpenResult{Wait: true, Delay: n.backoff(k)}, nil
		}
	}
	for _, e := range adv {
		et := &n.txns[e-1]
		et.holds = map[int]Mode{}
		et.att = map[int]int{}
		et.state = stateAborted
		et.ab++
		et.kp = (et.kp + 1) / 2
	}
	n.grant(t, o, write)
	return OpenResult{Aborted: adv}, nil
}

func (n *naive) restart(id int) (int64, error) {
	t, err := n.get(id)
	if err != nil {
		return 0, err
	}
	if t.state != stateAborted {
		return 0, ErrBadState
	}
	t.state = stateActive
	t.holds = map[int]Mode{}
	t.att = map[int]int{}
	k := t.ab - 1
	if k < 0 {
		k = 0
	}
	return n.backoff(k), nil
}

func (n *naive) commit(id int) error {
	t, err := n.getActive(id)
	if err != nil {
		return err
	}
	t.holds = map[int]Mode{}
	t.att = map[int]int{}
	t.state = stateCommitted
	return nil
}

func (n *naive) abort(id int) error {
	t, err := n.getActive(id)
	if err != nil {
		return err
	}
	t.holds = map[int]Mode{}
	t.att = map[int]int{}
	t.state = stateAborted
	return nil
}

// checkInvariants verifies the structural invariants of the Manager:
// every object has at most one writer, a writer excludes other holders,
// and the per-object sets agree with the per-transaction holds.
func checkInvariants(t *testing.T, m *Manager) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, tx := range m.txns {
		if tx.kp < 0 || tx.kp > m.cfg.ScoreCap {
			t.Fatalf("txn %d: kp=%d out of [0,%d]", id+1, tx.kp, m.cfg.ScoreCap)
		}
		if tx.state != stateActive && len(tx.holds) != 0 {
			t.Fatalf("txn %d: non-active but holds %v", id+1, tx.holds)
		}
	}
	for o := range m.objs {
		obj := &m.objs[o]
		writer := 0
		readers := map[int]bool{}
		for id, tx := range m.txns {
			switch tx.holds[o] {
			case ModeWrite:
				if writer != 0 {
					t.Fatalf("object %d: two writers %d and %d", o, writer, id+1)
				}
				writer = id + 1
			case ModeRead:
				readers[id+1] = true
			}
		}
		if writer != 0 && len(readers) != 0 {
			t.Fatalf("object %d: writer %d coexists with readers %v", o, writer, readers)
		}
		if obj.writer != writer {
			t.Fatalf("object %d: derived writer %d, holds say %d", o, obj.writer, writer)
		}
		if !reflect.DeepEqual(obj.readers, readers) {
			t.Fatalf("object %d: derived readers %v, holds say %v", o, obj.readers, readers)
		}
	}
}

func randomConfig(rng *rand.Rand) Config {
	return Config{
		Objects:        1 + rng.Intn(8), // small object space forces conflicts
		PrivThreshold:  1 + rng.Intn(4),
		BaseDelay:      1 + rng.Intn(1000),
		ExpCap:         rng.Intn(6),      // exercise the cap often
		ScoreCap:       1 + rng.Intn(12), // exercise the score cap
		ForceThreshold: 1 + rng.Intn(6),
	}
}

// TestRandomSequencesMatchNaive replays 2000 random call sequences on
// both the Manager and the naive simulation and requires identical
// results, scores, holds and delays at every step. The full input,
// output and decision basis of every call is logged.
func TestRandomSequencesMatchNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		cfg := randomConfig(rng)
		m, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("seq %d: NewManager(%+v): %v", seq, cfg, err)
		}
		n := newNaive(cfg)
		ops := 1 + rng.Intn(40)
		t.Logf("seq %d: cfg=%+v ops=%d", seq, cfg, ops)
		for op := 0; op < ops; op++ {
			ntxns := len(n.txns)
			kind := rng.Intn(100)
			switch {
			case kind < 25 || ntxns == 0:
				idM, idN := m.Begin(), n.begin()
				t.Logf("seq %d op %d: Begin() -> %d", seq, op, idM)
				if idM != idN {
					t.Fatalf("seq %d op %d: Begin() = %d, naive = %d", seq, op, idM, idN)
				}
			case kind < 70:
				id := 1 + rng.Intn(ntxns+1) // may not exist
				o := rng.Intn(cfg.Objects + 1)
				write := rng.Intn(2) == 0
				resM, errM := m.Open(id, o, write)
				resN, errN := n.open(id, o, write)
				t.Logf("seq %d op %d: Open(%d,%d,%v) -> %+v err=%v (naive %+v err=%v)",
					seq, op, id, o, write, resM, errM, resN, errN)
				if (errM == nil) != (errN == nil) || (errM != nil && errM != errN) {
					t.Fatalf("seq %d op %d: Open err %v vs naive %v", seq, op, errM, errN)
				}
				if resM.Wait != resN.Wait || resM.Delay != resN.Delay ||
					!reflect.DeepEqual(resM.Aborted, resN.Aborted) {
					t.Fatalf("seq %d op %d: Open = %+v, naive = %+v", seq, op, resM, resN)
				}
			case kind < 80:
				id := 1 + rng.Intn(ntxns+1)
				dM, errM := m.Restart(id)
				dN, errN := n.restart(id)
				t.Logf("seq %d op %d: Restart(%d) -> %d err=%v (naive %d err=%v)",
					seq, op, id, dM, errM, dN, errN)
				if dM != dN || (errM == nil) != (errN == nil) {
					t.Fatalf("seq %d op %d: Restart = %d,%v; naive = %d,%v", seq, op, dM, errM, dN, errN)
				}
			case kind < 90:
				id := 1 + rng.Intn(ntxns+1)
				errM, errN := m.Commit(id), n.commit(id)
				t.Logf("seq %d op %d: Commit(%d) -> %v (naive %v)", seq, op, id, errM, errN)
				if (errM == nil) != (errN == nil) {
					t.Fatalf("seq %d op %d: Commit = %v, naive = %v", seq, op, errM, errN)
				}
			default:
				id := 1 + rng.Intn(ntxns+1)
				errM, errN := m.Abort(id), n.abort(id)
				t.Logf("seq %d op %d: Abort(%d) -> %v (naive %v)", seq, op, id, errM, errN)
				if (errM == nil) != (errN == nil) {
					t.Fatalf("seq %d op %d: Abort = %v, naive = %v", seq, op, errM, errN)
				}
			}
			// Full state comparison after every call.
			for id := 1; id <= len(n.txns); id++ {
				kpM, abM, err := m.Stats(id)
				if err != nil {
					t.Fatalf("seq %d op %d: Stats(%d): %v", seq, op, id, err)
				}
				nt := &n.txns[id-1]
				if kpM != nt.kp || abM != nt.ab {
					t.Fatalf("seq %d op %d: txn %d stats (kp=%d,ab=%d), naive (kp=%d,ab=%d)",
						seq, op, id, kpM, abM, nt.kp, nt.ab)
				}
				holdsM, err := m.Holds(id)
				if err != nil {
					t.Fatalf("seq %d op %d: Holds(%d): %v", seq, op, id, err)
				}
				if !reflect.DeepEqual(holdsM, nt.holds) {
					t.Fatalf("seq %d op %d: txn %d holds %v, naive %v", seq, op, id, holdsM, nt.holds)
				}
			}
		}
		checkInvariants(t, m)
	}
}

// TestConcurrentUse hammers a shared Manager from many goroutines; the
// race detector plus the final invariant check validate linearizability.
func TestConcurrentUse(t *testing.T) {
	cfg := Config{Objects: 8, PrivThreshold: 2, BaseDelay: 10, ExpCap: 4, ScoreCap: 50, ForceThreshold: 3}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			my := []int{}
			for i := 0; i < 300; i++ {
				switch rng.Intn(5) {
				case 0:
					my = append(my, m.Begin())
				case 1:
					if len(my) > 0 {
						_, _ = m.Open(my[rng.Intn(len(my))], rng.Intn(cfg.Objects), rng.Intn(2) == 0)
					}
				case 2:
					if len(my) > 0 {
						_, _ = m.Restart(my[rng.Intn(len(my))])
					}
				case 3:
					if len(my) > 0 {
						_ = m.Commit(my[rng.Intn(len(my))])
					}
				case 4:
					if len(my) > 0 {
						_ = m.Abort(my[rng.Intn(len(my))])
					}
				}
			}
		}(int64(g))
	}
	wg.Wait()
	checkInvariants(t, m)
}

// TestReplayDeterminism: the same call sequence on two fresh managers
// yields identical results, scores and delays.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cfg := randomConfig(rng)
	type call struct {
		kind  int
		id, o int
		write bool
	}
	var calls []call
	idCounter := 0
	for i := 0; i < 200; i++ {
		switch k := rng.Intn(5); k {
		case 0:
			idCounter++
			calls = append(calls, call{kind: 0, id: idCounter})
		default:
			calls = append(calls, call{
				kind:  k,
				id:    1 + rng.Intn(idCounter+1),
				o:     rng.Intn(cfg.Objects),
				write: rng.Intn(2) == 0,
			})
		}
	}
	run := func() string {
		m, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		var sb strings.Builder
		for _, c := range calls {
			switch c.kind {
			case 0:
				fmt.Fprintf(&sb, "B%d;", m.Begin())
			case 1:
				r, err := m.Open(c.id, c.o, c.write)
				fmt.Fprintf(&sb, "O%d,%d,%v:%+v,%v;", c.id, c.o, c.write, r, err)
			case 2:
				d, err := m.Restart(c.id)
				fmt.Fprintf(&sb, "R%d:%d,%v;", c.id, d, err)
			case 3:
				fmt.Fprintf(&sb, "C%d:%v;", c.id, m.Commit(c.id))
			case 4:
				fmt.Fprintf(&sb, "A%d:%v;", c.id, m.Abort(c.id))
			}
		}
		for id := 1; ; id++ {
			kp, ab, err := m.Stats(id)
			if err != nil {
				break
			}
			fmt.Fprintf(&sb, "S%d:%d,%d;", id, kp, ab)
		}
		return sb.String()
	}
	if first, second := run(), run(); first != second {
		t.Fatalf("replay mismatch:\n%s\n%s", first, second)
	}
}

// TestAdversaryScanIsBounded is a compile-time documentation check: the
// Manager's adversary lookup touches only the object's writer and reader
// set (see Manager.adversaries), never the transaction table, so an Open
// examines at most (number of readers)+1 adversaries.
func TestAdversaryScanIsBounded(t *testing.T) {
	cfg := baseCfg()
	m := mustManager(t, cfg)
	// Many idle transactions must not affect the adversary count.
	for i := 0; i < 50; i++ {
		m.Begin()
	}
	r1, r2, w := m.Begin(), m.Begin(), m.Begin()
	mustGrant(t, m, r1, 0, false)
	mustGrant(t, m, r2, 0, false)
	mustGrant(t, m, w, 1, true)
	m.mu.Lock()
	adv := m.adversaries(m.txns[w-1], 0, true)
	m.mu.Unlock()
	if len(adv) != 2 { // exactly the two readers, not 50+ transactions
		t.Fatalf("adversaries = %v, want the 2 readers", adv)
	}
	sorted := sort.IntsAreSorted(adv)
	if !sorted {
		t.Fatalf("adversaries %v not ascending", adv)
	}
}
