package guard_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/guard"
)

// naive is a deliberately straightforward, step-by-step implementation
// of the specification, used as a test oracle. It recomputes window
// statistics by full scans and keeps per-id records in plain maps, so
// it shares no code with the optimized implementation under test.
type naive struct {
	cfg      guard.Config
	state    breaker.State
	epoch    uint64
	openedAt int64
	pIssued  int
	pOK      int
	window   []naiveCall
	nextID   int
	inSvc    int
	status   map[int]bulkhead.Outcome
	epochOf  map[int]uint64
	queue    []int
	enqAt    map[int]int64
	maxNow   int64
}

type naiveCall struct{ fail, slow bool }

func newNaive(cfg guard.Config) *naive {
	return &naive{
		cfg:     cfg,
		status:  map[int]bulkhead.Outcome{},
		epochOf: map[int]uint64{},
		enqAt:   map[int]int64{},
	}
}

func (n *naive) settle(now int64) {
	for len(n.queue) > 0 && n.enqAt[n.queue[0]]+n.cfg.Wt <= now {
		id := n.queue[0]
		n.queue = n.queue[1:]
		n.status[id] = bulkhead.TimedOut
	}
	if n.state == breaker.Open && now >= n.openedAt+n.cfg.O {
		n.state = breaker.HalfOpen
		n.epoch++
		n.pIssued, n.pOK = 0, 0
	}
}

func (n *naive) checkTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return guard.ErrInvalidTime
	}
	if now < n.maxNow {
		return guard.ErrClockRegression
	}
	return nil
}

func (n *naive) acquire(now int64) (guard.AcquireResult, int, error) {
	if err := n.checkTime(now); err != nil {
		return 0, 0, err
	}
	n.settle(now)
	n.maxNow = now
	switch n.state {
	case breaker.Open:
		return guard.RejectedOpen, 0, nil
	case breaker.HalfOpen:
		if n.pIssued >= n.cfg.H {
			return guard.RejectedHalfOpenFull, 0, nil
		}
	}
	if n.inSvc < n.cfg.C {
		n.nextID++
		n.inSvc++
		n.status[n.nextID] = bulkhead.InService
		n.epochOf[n.nextID] = n.epoch
		if n.state == breaker.HalfOpen {
			n.pIssued++
		}
		return guard.Granted, n.nextID, nil
	}
	if n.state == breaker.Closed && len(n.queue) < n.cfg.Q {
		n.nextID++
		n.queue = append(n.queue, n.nextID)
		n.enqAt[n.nextID] = now
		n.status[n.nextID] = bulkhead.Queued
		return guard.Queued, n.nextID, nil
	}
	return guard.RejectedFull, 0, nil
}

func (n *naive) release(id int, ok bool, dur, now int64) error {
	if id <= 0 || dur < 0 {
		return guard.ErrInvalidParam
	}
	if err := n.checkTime(now); err != nil {
		return err
	}
	if n.status[id] != bulkhead.InService {
		return guard.ErrNotInService
	}
	n.settle(now)
	n.maxNow = now
	n.inSvc--
	n.status[id] = bulkhead.Released
	if n.epochOf[id] == n.epoch {
		fail := !ok
		slow := dur >= n.cfg.S
		switch n.state {
		case breaker.Closed:
			n.window = append(n.window, naiveCall{fail, slow})
			if len(n.window) > n.cfg.N {
				n.window = n.window[1:]
			}
			count := len(n.window)
			fails, slows := 0, 0
			for _, c := range n.window {
				if c.fail {
					fails++
				}
				if c.slow {
					slows++
				}
			}
			if count >= n.cfg.M && (fails*100 >= n.cfg.F*count || slows*100 >= n.cfg.SR*count) {
				n.state = breaker.Open
				n.openedAt = now
				n.epoch++
				n.window = nil
				for _, qid := range n.queue {
					n.status[qid] = bulkhead.Revoked
				}
				n.queue = nil
			}
		case breaker.HalfOpen:
			if fail || slow {
				n.state = breaker.Open
				n.openedAt = now
				n.epoch++
				n.window = nil
			} else {
				n.pOK++
				if n.pOK >= n.cfg.H {
					n.state = breaker.Closed
					n.epoch++
					n.window = nil
				}
			}
		}
	}
	if n.state == breaker.Closed {
		for n.inSvc < n.cfg.C && len(n.queue) > 0 {
			qid := n.queue[0]
			n.queue = n.queue[1:]
			n.status[qid] = bulkhead.InService
			n.epochOf[qid] = n.epoch
			n.inSvc++
		}
	}
	return nil
}

func (n *naive) statusOf(id int, now int64) (bulkhead.Outcome, error) {
	if id <= 0 {
		return 0, guard.ErrInvalidParam
	}
	if err := n.checkTime(now); err != nil {
		return 0, err
	}
	if _, known := n.status[id]; !known {
		return 0, guard.ErrUnknownID
	}
	n.settle(now)
	n.maxNow = now
	return n.status[id], nil
}

func (n *naive) snapshot() guard.Snapshot {
	fails, slows := 0, 0
	for _, c := range n.window {
		if c.fail {
			fails++
		}
		if c.slow {
			slows++
		}
	}
	return guard.Snapshot{
		State: n.state, Epoch: n.epoch, RingCount: len(n.window),
		Failures: fails, Slows: slows, InService: n.inSvc, QueueLen: len(n.queue),
	}
}

type randOp struct {
	kind int // 0 acquire, 1 release, 2 status
	id   int
	ok   bool
	dur  int64
	now  int64
}

func randomConfig(rng *rand.Rand) guard.Config {
	n := 1 + rng.Intn(6)
	return guard.Config{
		N:  n,
		M:  1 + rng.Intn(n),
		F:  1 + rng.Intn(100),
		SR: 1 + rng.Intn(100),
		S:  1 + int64(rng.Intn(8)),
		O:  1 + int64(rng.Intn(10)),
		Wt: 1 + int64(rng.Intn(8)),
		H:  1 + rng.Intn(3),
		C:  1 + rng.Intn(3),
		Q:  rng.Intn(3),
	}
}

func randomOps(rng *rand.Rand, count int) []randOp {
	ops := make([]randOp, 0, count)
	now := int64(0)
	for i := 0; i < count; i++ {
		switch r := rng.Intn(100); {
		case r < 85:
			now += int64(rng.Intn(8))
		case r < 93:
			now -= int64(rng.Intn(4)) // possible regression / negative
		case r < 97:
			now = 1_000_000_000_000_000 + int64(rng.Intn(3)) // invalid time
		default:
			now = int64(rng.Intn(20)) // possible regression
		}
		op := randOp{now: now}
		switch k := rng.Intn(100); {
		case k < 45:
			op.kind = 0
		case k < 80:
			op.kind = 1
		default:
			op.kind = 2
		}
		if op.kind != 0 {
			switch rng.Intn(10) {
			case 0:
				op.id = 0
			case 1:
				op.id = -1 - rng.Intn(5)
			case 2:
				op.id = 30 + rng.Intn(20)
			default:
				op.id = rng.Intn(30)
			}
		}
		if op.kind == 1 {
			op.ok = rng.Intn(100) < 70
			op.dur = int64(rng.Intn(26)) - 1 // -1 .. 24
		}
		ops = append(ops, op)
	}
	return ops
}

// TestRandomSequencesMatchNaive replays 2000 random operation sequences
// against the naive oracle and a second guard instance (determinism),
// comparing every result, error and snapshot, logging inputs, outputs
// and the decision basis for each step.
func TestRandomSequencesMatchNaive(t *testing.T) {
	const trials = 2000
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(trial + 1)))
		cfg := randomConfig(rng)
		ops := randomOps(rng, 20+rng.Intn(40))
		g, err := guard.New(cfg)
		if err != nil {
			t.Fatalf("trial %d: New(%+v): %v", trial, cfg, err)
		}
		replay, err := guard.New(cfg)
		if err != nil {
			t.Fatalf("trial %d: New(%+v): %v", trial, cfg, err)
		}
		n := newNaive(cfg)
		for i, op := range ops {
			switch op.kind {
			case 0:
				res, id, err := g.Acquire(op.now)
				res2, id2, err2 := replay.Acquire(op.now)
				nres, nid, nerr := n.acquire(op.now)
				t.Logf("trial %d op %02d Acquire(now=%d) => guard(%s,%d,%v) naive(%s,%d,%v) | cfg=%+v 依据=%+v",
					trial, i, op.now, res, id, err, nres, nid, nerr, cfg, g.Snapshot())
				if res != nres || id != nid || !errors.Is(err, nerr) {
					t.Fatalf("trial %d op %d Acquire: guard(%s,%d,%v) != naive(%s,%d,%v)",
						trial, i, res, id, err, nres, nid, nerr)
				}
				if res != res2 || id != id2 || !errors.Is(err2, err) {
					t.Fatalf("trial %d op %d Acquire: replay diverged", trial, i)
				}
			case 1:
				err := g.Release(op.id, op.ok, op.dur, op.now)
				err2 := replay.Release(op.id, op.ok, op.dur, op.now)
				nerr := n.release(op.id, op.ok, op.dur, op.now)
				t.Logf("trial %d op %02d Release(id=%d, ok=%v, dur=%d, now=%d) => guard(%v) naive(%v) | 依据=%+v",
					trial, i, op.id, op.ok, op.dur, op.now, err, nerr, g.Snapshot())
				if !errors.Is(err, nerr) {
					t.Fatalf("trial %d op %d Release: guard(%v) != naive(%v)", trial, i, err, nerr)
				}
				if !errors.Is(err2, err) {
					t.Fatalf("trial %d op %d Release: replay diverged", trial, i)
				}
			case 2:
				outcome, err := g.Status(op.id, op.now)
				outcome2, err2 := replay.Status(op.id, op.now)
				noutcome, nerr := n.statusOf(op.id, op.now)
				t.Logf("trial %d op %02d Status(id=%d, now=%d) => guard(%s,%v) naive(%s,%v) | 依据=%+v",
					trial, i, op.id, op.now, outcome, err, noutcome, nerr, g.Snapshot())
				if outcome != noutcome || !errors.Is(err, nerr) {
					t.Fatalf("trial %d op %d Status: guard(%s,%v) != naive(%s,%v)",
						trial, i, outcome, err, noutcome, nerr)
				}
				if outcome != outcome2 || !errors.Is(err2, err) {
					t.Fatalf("trial %d op %d Status: replay diverged", trial, i)
				}
			}
			if got, want := g.Snapshot(), n.snapshot(); got != want {
				t.Fatalf("trial %d op %d: snapshot got %+v, naive %+v", trial, i, got, want)
			}
			if got, want := g.Snapshot(), replay.Snapshot(); got != want {
				t.Fatalf("trial %d op %d: replay snapshot got %+v, want %+v", trial, i, want, got)
			}
			checkInvariants(t, g, cfg)
		}
	}
}

// TestConcurrentUse hammers one guard from many goroutines; with -race
// it proves data-race freedom, and the final invariants prove the
// execution was equivalent to some serial order.
func TestConcurrentUse(t *testing.T) {
	cfg := guard.Config{N: 10, M: 3, F: 50, SR: 50, S: 5, O: 20, Wt: 10, H: 2, C: 4, Q: 8}
	g, err := guard.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			now := int64(0)
			for i := 0; i < 500; i++ {
				now += int64(rng.Intn(5))
				switch rng.Intn(3) {
				case 0:
					_, _, _ = g.Acquire(now)
				case 1:
					_ = g.Release(1+rng.Intn(60), rng.Intn(2) == 0, int64(rng.Intn(20)), now)
				case 2:
					_, _ = g.Status(1+rng.Intn(60), now)
				}
			}
		}(int64(w + 1))
	}
	wg.Wait()
	checkInvariants(t, g, cfg)
	t.Logf("final snapshot after concurrent run: %+v", g.Snapshot())
}
