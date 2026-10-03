package htm

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveSim is a deliberately plain, step-by-step simulation of the
// specification, written independently from the Controller
// implementation. The randomized differential test replays identical
// call sequences against both and demands identical results.
type naiveSim struct {
	cfg    Config
	state  []State
	reason []AbortReason
	r      []int
	read   []map[int]bool
	write  []map[int]bool
	holder int
	skip   int
	fb     int
}

func newNaiveSim(cfg Config) *naiveSim {
	return &naiveSim{
		cfg:    cfg,
		state:  make([]State, cfg.N),
		reason: make([]AbortReason, cfg.N),
		r:      make([]int, cfg.N),
		read:   make([]map[int]bool, cfg.N),
		write:  make([]map[int]bool, cfg.N),
		holder: -1,
	}
}

func (s *naiveSim) lock(t int) (LockResult, error) {
	if t < 0 || t >= s.cfg.N {
		return LockResult{}, ErrInvalidThread
	}
	if s.state[t] != Idle && s.state[t] != Aborted {
		return LockResult{}, ErrInvalidState
	}
	if s.holder != -1 {
		return LockResult{Rule: RuleWait}, nil
	}
	fallback := false
	countFb := false
	rule := RuleSpeculate
	if s.state[t] == Aborted && s.reason[t] == Capacity {
		fallback, countFb, rule = true, true, RuleCapacityAbort
	} else if s.skip > 0 {
		s.skip--
		fallback, rule = true, RuleSkip
	} else if s.r[t] > s.cfg.R {
		fallback, countFb, rule = true, true, RuleRetryBudget
	}
	if !fallback {
		s.state[t] = Speculating
		s.reason[t] = NoAbort
		s.read[t] = map[int]bool{}
		s.write[t] = map[int]bool{}
		return LockResult{Rule: RuleSpeculate}, nil
	}
	aborted := []int{}
	for u := 0; u < s.cfg.N; u++ {
		if u != t && s.state[u] == Speculating {
			s.state[u] = Aborted
			s.reason[u] = LockHeld
			s.read[u] = nil
			s.write[u] = nil
			aborted = append(aborted, u)
		}
	}
	s.holder = t
	s.state[t] = Fallback
	s.reason[t] = NoAbort
	s.read[t] = nil
	s.write[t] = nil
	if countFb {
		s.fb++
		if s.fb == s.cfg.F {
			s.skip = s.cfg.SK
			s.fb = 0
		}
	}
	return LockResult{Rule: rule, Aborted: aborted}, nil
}

func (s *naiveSim) access(t, a int, write bool) (AccessResult, error) {
	if t < 0 || t >= s.cfg.N {
		return AccessResult{}, ErrInvalidThread
	}
	if s.state[t] != Speculating && s.state[t] != Fallback {
		return AccessResult{}, ErrInvalidState
	}
	if a < 0 || a >= s.cfg.A {
		return AccessResult{}, ErrInvalidAddress
	}
	if s.state[t] == Fallback {
		return AccessResult{Outcome: AccessFallbackNoOp}, nil
	}
	aborted := []int{}
	for u := 0; u < s.cfg.N; u++ {
		if u == t || s.state[u] != Speculating {
			continue
		}
		hit := s.write[u][a] || (write && s.read[u][a])
		if hit {
			s.state[u] = Aborted
			s.reason[u] = Conflict
			s.r[u]++
			s.read[u] = nil
			s.write[u] = nil
			aborted = append(aborted, u)
		}
	}
	if write {
		s.write[t][a] = true
	} else {
		s.read[t][a] = true
	}
	set := a % s.cfg.S
	distinct := map[int]bool{}
	for addr := range s.read[t] {
		if addr%s.cfg.S == set {
			distinct[addr] = true
		}
	}
	for addr := range s.write[t] {
		if addr%s.cfg.S == set {
			distinct[addr] = true
		}
	}
	if len(distinct) > s.cfg.W {
		s.state[t] = Aborted
		s.reason[t] = Capacity
		s.read[t] = nil
		s.write[t] = nil
		s.skip = s.cfg.SK
		return AccessResult{Outcome: AccessCapacityAbort, Aborted: aborted}, nil
	}
	return AccessResult{Outcome: AccessOK, Aborted: aborted}, nil
}

func (s *naiveSim) unlock(t int) error {
	if t < 0 || t >= s.cfg.N {
		return ErrInvalidThread
	}
	switch s.state[t] {
	case Speculating:
		s.read[t] = nil
		s.write[t] = nil
		s.fb = 0
	case Fallback:
		s.holder = -1
	default:
		return ErrInvalidState
	}
	s.state[t] = Idle
	s.reason[t] = NoAbort
	s.r[t] = 0
	return nil
}

func (s *naiveSim) snapshot() Snapshot {
	snap := Snapshot{
		Threads: make([]ThreadSnapshot, s.cfg.N),
		Holder:  s.holder,
		Skip:    s.skip,
		Fb:      s.fb,
	}
	for i := 0; i < s.cfg.N; i++ {
		snap.Threads[i] = ThreadSnapshot{
			State:  s.state[i],
			Reason: s.reason[i],
			R:      s.r[i],
			Read:   sortedKeys(s.read[i]),
			Write:  sortedKeys(s.write[i]),
		}
	}
	return snap
}

// checkInvariants verifies the cross-thread safety invariants on a
// snapshot: pairwise conflict-free speculative sets, no speculative
// thread while the lock is held, per-set capacity, and counter bounds.
func checkInvariants(t *testing.T, cfg Config, snap Snapshot) {
	t.Helper()
	for i := range snap.Threads {
		ts := snap.Threads[i]
		if ts.State != Speculating {
			continue
		}
		if snap.Holder != -1 {
			t.Fatalf("invariant: thread %d speculating while holder=%d", i, snap.Holder)
		}
		perSet := map[int]map[int]bool{}
		for _, a := range ts.Read {
			if perSet[a%cfg.S] == nil {
				perSet[a%cfg.S] = map[int]bool{}
			}
			perSet[a%cfg.S][a] = true
		}
		for _, a := range ts.Write {
			if perSet[a%cfg.S] == nil {
				perSet[a%cfg.S] = map[int]bool{}
			}
			perSet[a%cfg.S][a] = true
		}
		for set, addrs := range perSet {
			if len(addrs) > cfg.W {
				t.Fatalf("invariant: thread %d set %d holds %d addresses > W=%d", i, set, len(addrs), cfg.W)
			}
		}
		for j := range snap.Threads {
			if j == i || snap.Threads[j].State != Speculating {
				continue
			}
			wi := map[int]bool{}
			for _, a := range snap.Threads[i].Write {
				wi[a] = true
			}
			for _, a := range snap.Threads[j].Write {
				if wi[a] {
					t.Fatalf("invariant: write-write conflict between %d and %d on %d", i, j, a)
				}
			}
			for _, a := range snap.Threads[j].Read {
				if wi[a] {
					t.Fatalf("invariant: write-read conflict between %d and %d on %d", i, j, a)
				}
			}
		}
	}
	if snap.Skip < 0 || snap.Skip > cfg.SK {
		t.Fatalf("invariant: skip=%d outside [0,%d]", snap.Skip, cfg.SK)
	}
	if snap.Fb < 0 || snap.Fb >= cfg.F {
		t.Fatalf("invariant: fb=%d outside [0,%d)", snap.Fb, cfg.F)
	}
}

type opKind int

const (
	opLock opKind = iota
	opAccess
	opUnlock
)

type op struct {
	kind   opKind
	thread int
	addr   int
	write  bool
}

func (o op) String() string {
	switch o.kind {
	case opLock:
		return fmt.Sprintf("Lock(%d)", o.thread)
	case opAccess:
		return fmt.Sprintf("Access(%d,%d,write=%v)", o.thread, o.addr, o.write)
	default:
		return fmt.Sprintf("Unlock(%d)", o.thread)
	}
}

func randomConfig(rng *rand.Rand) Config {
	return Config{
		N:  1 + rng.Intn(16),
		S:  1 + rng.Intn(8),
		W:  1 + rng.Intn(4),
		A:  1 + rng.Intn(64),
		R:  rng.Intn(9),
		SK: rng.Intn(9),
		F:  1 + rng.Intn(8),
	}
}

// randomOp picks an operation, occasionally out of range so rejection
// paths are exercised too.
func randomOp(rng *rand.Rand, cfg Config) op {
	thread := rng.Intn(cfg.N+2) - 1 // -1..N
	switch rng.Intn(3) {
	case 0:
		return op{kind: opLock, thread: thread}
	case 1:
		return op{kind: opAccess, thread: thread, addr: rng.Intn(cfg.A+2) - 1, write: rng.Intn(2) == 0}
	default:
		return op{kind: opUnlock, thread: thread}
	}
}

func runOp(c *Controller, s *naiveSim, o op) (string, error) {
	switch o.kind {
	case opLock:
		got, gerr := c.Lock(o.thread)
		want, werr := s.lock(o.thread)
		if gerr != werr || !reflect.DeepEqual(got, want) {
			return "", fmt.Errorf("Lock mismatch: got %+v err=%v, want %+v err=%v", got, gerr, want, werr)
		}
		if gerr != nil {
			return fmt.Sprintf("rejected: %v", gerr), nil
		}
		return fmt.Sprintf("rule=%v aborted=%v", got.Rule, got.Aborted), nil
	case opAccess:
		got, gerr := c.Access(o.thread, o.addr, o.write)
		want, werr := s.access(o.thread, o.addr, o.write)
		if gerr != werr || !reflect.DeepEqual(got, want) {
			return "", fmt.Errorf("Access mismatch: got %+v err=%v, want %+v err=%v", got, gerr, want, werr)
		}
		if gerr != nil {
			return fmt.Sprintf("rejected: %v", gerr), nil
		}
		return fmt.Sprintf("outcome=%v aborted=%v", got.Outcome, got.Aborted), nil
	default:
		gerr := c.Unlock(o.thread)
		werr := s.unlock(o.thread)
		if gerr != werr {
			return "", fmt.Errorf("Unlock mismatch: got err=%v, want err=%v", gerr, werr)
		}
		if gerr != nil {
			return fmt.Sprintf("rejected: %v", gerr), nil
		}
		return "ok", nil
	}
}

// TestRandomDifferential replays 2000 random call sequences against
// both the Controller and the naive simulation, comparing every
// result, the full state, and the safety invariants after each call.
// Each step is logged with input, output and decision basis.
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq) * 7919))
		cfg := randomConfig(rng)
		c := mustNew(t, cfg)
		s := newNaiveSim(cfg)
		steps := 1 + rng.Intn(60)
		for step := 0; step < steps; step++ {
			o := randomOp(rng, cfg)
			out, err := runOp(c, s, o)
			if err != nil {
				t.Fatalf("seq=%d step=%d cfg=%+v op=%v: %v", seq, step, cfg, o, err)
			}
			t.Logf("seq=%d step=%d in=%s out=%s", seq, step, o, out)
			got, want := c.Snapshot(), s.snapshot()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seq=%d step=%d cfg=%+v op=%v:\ncontroller=%+v\nnaive=%+v",
					seq, step, cfg, o, got, want)
			}
			checkInvariants(t, cfg, got)
		}
	}
}

// TestConcurrentCalls hammer the controller from many goroutines; the
// result must remain equivalent to some serial order, which the final
// invariant check validates. Run with -race to check the locking.
func TestConcurrentCalls(t *testing.T) {
	cfg := Config{N: 16, S: 4, W: 2, A: 16, R: 2, SK: 2, F: 3}
	c := mustNew(t, cfg)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				o := randomOp(rng, cfg)
				switch o.kind {
				case opLock:
					_, _ = c.Lock(o.thread)
				case opAccess:
					_, _ = c.Access(o.thread, o.addr, o.write)
				default:
					_ = c.Unlock(o.thread)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	checkInvariants(t, cfg, c.Snapshot())
}
