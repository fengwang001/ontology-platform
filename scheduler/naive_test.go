package scheduler

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// naive is a literal, step-by-step implementation of the specification used
// as the reference model. Its decision scans every ready task.
type naiveTask struct {
	id, p, th, rem, pc int
	wt                 int64
	seq                uint64
}

type naive struct {
	W, Bmax, M, Tmax int
	now              int64
	seqNext          uint64
	tasks            map[int]*naiveTask // running + ready
	running          *naiveTask
	examined         int64
}

func newNaive(W, Bmax, M, Tmax int) *naive {
	return &naive{W: W, Bmax: Bmax, M: M, Tmax: Tmax, seqNext: 1, tasks: map[int]*naiveTask{}}
}

func (n *naive) add(id, p, th, w int) error {
	if id < 1 || p < 0 || p > 255 || th < p || th > 255 || w < 1 || w > 1_000_000 {
		return ErrInvalidParam
	}
	if _, ok := n.tasks[id]; ok {
		return ErrDuplicate
	}
	if len(n.tasks) >= n.Tmax {
		return ErrFull
	}
	n.tasks[id] = &naiveTask{id: id, p: p, th: th, rem: w, seq: n.seqNext}
	n.seqNext++
	return nil
}

func (n *naive) eff(t *naiveTask) int {
	b := t.wt / int64(n.W)
	if b > int64(n.Bmax) {
		b = int64(n.Bmax)
	}
	return t.p + int(b)
}

func (n *naive) step() (runID, compID int) {
	// Decision: linear scan over all ready tasks.
	var sigma *naiveTask
	for _, t := range n.tasks {
		if t == n.running {
			continue
		}
		n.examined++
		if sigma == nil || n.eff(t) > n.eff(sigma) ||
			(n.eff(t) == n.eff(sigma) && t.seq < sigma.seq) {
			sigma = t
		}
	}
	shield := InfShield
	if n.running != nil && n.running.pc < n.M {
		shield = n.running.th
	}
	switch {
	case n.running == nil && sigma != nil:
		sigma.wt = 0
		n.running = sigma
	case n.running != nil && sigma != nil && n.eff(sigma) > shield:
		n.running.pc++
		n.running.wt = 0
		sigma.wt = 0
		n.running = sigma
	}
	// Execution.
	if n.running != nil {
		runID = n.running.id
		n.running.rem--
		if n.running.rem == 0 {
			compID = n.running.id
			delete(n.tasks, compID)
			n.running = nil
		}
	}
	for _, t := range n.tasks {
		if t != n.running {
			t.wt++
		}
	}
	n.now++
	return runID, compID
}

// TestRandomAgainstNaive replays 2000 random configurations and arrival
// sequences against the reference model, comparing results and full state
// after every operation.
func TestRandomAgainstNaive(t *testing.T) {
	const iterations = 2000
	for it := 0; it < iterations; it++ {
		rng := rand.New(rand.NewSource(int64(it) + 1))
		W := 1 + rng.Intn(6)
		Bmax := rng.Intn(4)
		M := 1 + rng.Intn(3)
		Tmax := 1 + rng.Intn(10)

		s, err := New(W, Bmax, M, Tmax)
		if err != nil {
			t.Fatalf("iter %d: New: %v", it, err)
		}
		ref := newNaive(W, Bmax, M, Tmax)

		runTicks := 0           // ticks with a runner
		finishedWork := 0       // total work of completed tasks
		addedWork := 0          // total work of accepted adds
		workOf := map[int]int{} // id -> w of the latest accepted Add

		ops := 40 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			if rng.Intn(100) < 45 {
				id := 1 + rng.Intn(2*Tmax+2)
				p := rng.Intn(8)
				th := p + rng.Intn(4)
				w := 1 + rng.Intn(6)
				if rng.Intn(100) < 10 { // inject invalid parameters
					switch rng.Intn(4) {
					case 0:
						id = 0
					case 1:
						p, th = 7, 2
					case 2:
						w = 0
					case 3:
						p = 300
					}
				}
				gotErr := s.Add(id, p, th, w)
				wantErr := ref.add(id, p, th, w)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("iter %d op %d: Add(%d,%d,%d,%d) err=%v, naive=%v",
						it, op, id, p, th, w, gotErr, wantErr)
				}
				if wantErr == nil {
					addedWork += w
					workOf[id] = w
				}
				continue
			}
			run, comp := s.Step()
			refRun, refComp := ref.step()
			if run != refRun || comp != refComp {
				t.Fatalf("iter %d op %d: Step=(%d,%d), naive=(%d,%d)",
					it, op, run, comp, refRun, refComp)
			}
			if run != 0 {
				runTicks++
			}
			if comp != 0 {
				finishedWork += workOf[comp]
			}
			compareState(t, it, op, s, ref)
			checkInvariants(t, it, op, s, M, runTicks, addedWork, finishedWork)
			_ = finishedWork
		}
	}
}

func compareState(t *testing.T, it, op int, s *Scheduler, ref *naive) {
	t.Helper()
	if s.now != ref.now || s.seqNext != ref.seqNext {
		t.Fatalf("iter %d op %d: now/seq (%d,%d) != naive (%d,%d)",
			it, op, s.now, s.seqNext, ref.now, ref.seqNext)
	}
	if len(s.tasks) != len(ref.tasks) {
		t.Fatalf("iter %d op %d: %d tasks != naive %d", it, op, len(s.tasks), len(ref.tasks))
	}
	ids := make([]int, 0, len(ref.tasks))
	for id := range ref.tasks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		st, ok := s.tasks[id]
		if !ok {
			t.Fatalf("iter %d op %d: task %d missing in scheduler", it, op, id)
		}
		nt := ref.tasks[id]
		if st.wt != nt.wt || st.pc != nt.pc || st.rem != nt.rem || st.seq != nt.seq {
			t.Fatalf("iter %d op %d: task %d state (wt=%d,pc=%d,rem=%d,seq=%d) != naive (wt=%d,pc=%d,rem=%d,seq=%d)",
				it, op, id, st.wt, st.pc, st.rem, st.seq, nt.wt, nt.pc, nt.rem, nt.seq)
		}
		_, ready := s.ready[id]
		if (s.running == st) == ready {
			t.Fatalf("iter %d op %d: task %d both/neither running and ready", it, op, id)
		}
		if (ref.running == nt) != (s.running == st) {
			t.Fatalf("iter %d op %d: task %d running mismatch", it, op, id)
		}
	}
}

func checkInvariants(t *testing.T, it, op int, s *Scheduler, M, runTicks, addedWork, finishedWork int) {
	t.Helper()
	// At most one runner; every registered task is either running or ready.
	for id, st := range s.tasks {
		_, ready := s.ready[id]
		if s.running != st && !ready {
			t.Fatalf("iter %d op %d: task %d neither running nor ready", it, op, id)
		}
	}
	// Executed ticks equal added work minus remaining work (completed
	// tasks have rem == 0 accounted via their removal from the registry).
	remaining := 0
	for _, st := range s.tasks {
		remaining += st.rem
	}
	if executed := addedWork - remaining; executed != runTicks {
		t.Fatalf("iter %d op %d: executed=%d != runTicks=%d", it, op, executed, runTicks)
	}
	// A task with pc >= M can never be preempted while running.
	d := s.last
	if d.Preempted {
		for _, st := range s.tasks {
			if st.id == d.RunnerID && st.pc > M {
				t.Fatalf("iter %d op %d: protected task %d preempted (pc=%d, M=%d)",
					it, op, st.id, st.pc, M)
			}
		}
	}
	// Bonus buckets are consistent with wt, and every ready task sits in
	// the bucket of its clamped bonus.
	for id, st := range s.ready {
		if st.bonus != s.bonusOf(st.wt) {
			t.Fatalf("iter %d op %d: task %d bonus=%d, want %d (wt=%d)",
				it, op, id, st.bonus, s.bonusOf(st.wt), st.wt)
		}
	}
	for b := 0; b <= s.bmax; b++ {
		for _, st := range s.buckets[b].ts {
			if st.bonus != b {
				t.Fatalf("iter %d op %d: task %d in bucket %d with bonus %d",
					it, op, st.id, b, st.bonus)
			}
		}
	}
}

// TestExaminedScalability shows the decision never scans all ready tasks:
// with 100 and 10000 ready tasks (all crossing the bonus bands at the same
// time) the average number of examined candidates per Step stays constant,
// so the ratio stays far below 4 (a linear scan would give ~100).
func TestExaminedScalability(t *testing.T) {
	avg := func(n, steps int) float64 {
		s, err := New(3, 2, 1, n+1)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := s.Add(1, 255, 255, steps+10); err != nil { // permanent runner
			t.Fatalf("Add runner: %v", err)
		}
		for i := 0; i < n; i++ {
			if err := s.Add(1000+i, 1+i%5, 255, 1_000_000); err != nil {
				t.Fatalf("Add waiter %d: %v", i, err)
			}
		}
		for k := 0; k < steps; k++ {
			s.Step()
		}
		return float64(s.Examined()) / float64(steps)
	}
	const steps = 300
	a100 := avg(100, steps)
	a10000 := avg(10000, steps)
	ratio := a10000 / a100
	t.Logf("avg examined per Step: n=100 -> %.3f, n=10000 -> %.3f, ratio=%.3f (linear scan would be ~100)",
		a100, a10000, ratio)
	if ratio >= 4 {
		t.Fatalf("examined ratio %.3f >= 4", ratio)
	}
}

// TestConcurrency hammers the scheduler from many goroutines; with -race
// this proves mutual exclusion, and the final clock must equal the number
// of completed Step calls (every Step is serializable).
func TestConcurrency(t *testing.T) {
	s := mustNew(t, 3, 2, 2, 64)
	var steps int64
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				// may fail with full/duplicate; errors are fine here
				_ = s.Add(1_000_000*g+i+1, i%6, 255, 1+i%3)
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				s.Step()
				atomic.AddInt64(&steps, 1)
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				_ = s.Now()
				_ = s.Examined()
				_ = s.LastDecision()
			}
		}()
	}
	wg.Wait()
	if got := s.Now(); got != steps {
		t.Fatalf("now = %d, want %d (one per Step)", got, steps)
	}
}
