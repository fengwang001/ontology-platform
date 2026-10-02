package scheduler

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// naive is a direct transcription of the spec: it scans every ready task on
// every decision and tracks wt explicitly. The real Scheduler must agree with
// it on every observable result.
type naive struct {
	W, Bmax, M, Tmax int
	now, seq         int
	tasks            map[int]*ntask
	ready            map[int]bool
	running          *ntask
}

type ntask struct {
	id, p, th, work, rem, seq, wt, pc int
}

func newNaive(w, bmax, m, tmax int) *naive {
	return &naive{W: w, Bmax: bmax, M: m, Tmax: tmax, tasks: map[int]*ntask{}, ready: map[int]bool{}}
}

func (n *naive) add(id, p, th, w int) error {
	if id < 1 || p < 0 || p > 255 || th < p || th > 255 || w < 1 || w > 1_000_000 {
		return ErrInvalidTask
	}
	if _, ok := n.tasks[id]; ok {
		return ErrTaskExists
	}
	if len(n.tasks) >= n.Tmax {
		return ErrFull
	}
	n.seq++
	n.tasks[id] = &ntask{id: id, p: p, th: th, work: w, rem: w, seq: n.seq}
	n.ready[id] = true
	return nil
}

func (n *naive) step() (run, done int) {
	// Decision, using wt as of the tick start.
	var sigma *ntask
	se := 0
	for id := range n.ready {
		cand := n.tasks[id]
		e := cand.p + min(n.Bmax, cand.wt/n.W)
		if sigma == nil || e > se || (e == se && cand.seq < sigma.seq) {
			sigma, se = cand, e
		}
	}
	sh := infinity
	if n.running != nil && n.running.pc < n.M {
		sh = n.running.th
	}
	if sigma != nil {
		switch {
		case n.running == nil:
			n.dispatch(sigma)
		case se > sh:
			n.running.pc++
			n.running.wt = 0
			n.ready[n.running.id] = true
			n.dispatch(sigma)
		}
	}
	// Execution.
	if n.running != nil {
		run = n.running.id
		n.running.rem--
		if n.running.rem == 0 {
			done = n.running.id
			delete(n.tasks, done)
			n.running = nil
		}
	}
	for id := range n.ready {
		n.tasks[id].wt++
	}
	n.now++
	return run, done
}

func (n *naive) dispatch(t *ntask) {
	delete(n.ready, t.id)
	t.wt = 0
	n.running = t
}

// TestAgainstNaive replays 2000 random parameter/operation sequences against
// both implementations and requires identical observable behavior plus
// consistent internal wait/preemption counters.
func TestAgainstNaive(t *testing.T) {
	const groups = 2000
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g) + 1))
		w, bmax, m, tmax := 1+rng.Intn(4), rng.Intn(4), 1+rng.Intn(3), 1+rng.Intn(6)
		s, err := NewScheduler(w, bmax, m, tmax)
		if err != nil {
			t.Fatalf("group %d: NewScheduler: %v", g, err)
		}
		if g == 0 { // sample per-tick decision log: candidate e, shield, reason
			s.SetLogger(func(format string, args ...any) { t.Logf(format, args...) })
		}
		n := newNaive(w, bmax, m, tmax)

		executed := map[int]int{} // id -> ticks run since arrival
		work := map[int]int{}     // id -> workload of current incarnation

		ops := 20 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			if rng.Intn(100) < 55 {
				id := 1 + rng.Intn(5)
				p, th, wk := rng.Intn(5), 0, 1+rng.Intn(5)
				th = p + rng.Intn(6-p)
				if rng.Intn(100) < 8 { // inject invalid parameters
					p, th, wk = -1-rng.Intn(3), rng.Intn(300), rng.Intn(3)
				}
				gotErr := s.Add(id, p, th, wk)
				wantErr := n.add(id, p, th, wk)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("group %d op %d: Add(%d,%d,%d,%d) err=%v, naive=%v",
						g, op, id, p, th, wk, gotErr, wantErr)
				}
				if gotErr == nil {
					executed[id] = 0
					work[id] = wk
				}
				continue
			}
			run, done := s.Step()
			nrun, ndone := n.step()
			if run != nrun || done != ndone {
				t.Fatalf("group %d op %d: Step()=(%d,%d), naive=(%d,%d)",
					g, op, run, done, nrun, ndone)
			}
			if run != 0 {
				executed[run]++
			}
			if done != 0 {
				if executed[done] != work[done] {
					t.Fatalf("group %d: task %d executed %d ticks, work=%d",
						g, done, executed[done], work[done])
				}
				delete(executed, done)
				delete(work, done)
			}
			// Internal counters must match the naive model exactly.
			if s.now != n.now || s.seq != n.seq || len(s.tasks) != len(n.tasks) {
				t.Fatalf("group %d op %d: clock/seq/count drift", g, op)
			}
			for id, nt := range n.tasks {
				rt := s.tasks[id]
				if rt == nil {
					t.Fatalf("group %d: task %d missing in scheduler", g, id)
				}
				if rt.pc != nt.pc || rt.rem != nt.rem || rt.seq != nt.seq {
					t.Fatalf("group %d: task %d pc/rem/seq drift", g, id)
				}
				if n.ready[id] {
					if wt := s.now - rt.epoch; wt != nt.wt {
						t.Fatalf("group %d: task %d wt=%d, naive=%d", g, id, wt, nt.wt)
					}
					if want := nt.p + min(n.Bmax, nt.wt/n.W); rt.e != want {
						t.Fatalf("group %d: task %d e=%d, want %d", g, id, rt.e, want)
					}
				}
			}
		}
		// Invariant: executed ticks == work - remaining for registered tasks.
		for id, ex := range executed {
			rt := s.tasks[id]
			if rt == nil {
				t.Fatalf("group %d: task %d tracked but not registered", g, id)
			}
			if rt.work-rt.rem != ex {
				t.Fatalf("group %d: task %d work-rem=%d, executed=%d",
					g, id, rt.work-rt.rem, ex)
			}
		}
		if s.running != nil && s.running.state != stateRunning {
			t.Fatalf("group %d: corrupted running pointer", g)
		}
	}
}

// TestReplayDeterminism runs the same operation sequence twice and requires
// identical run/completion traces.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	type op struct {
		add          bool
		id, p, th, w int
	}
	var ops []op
	for i := 0; i < 200; i++ {
		if rng.Intn(100) < 50 {
			p := rng.Intn(6)
			ops = append(ops, op{true, 1 + rng.Intn(8), p, p + rng.Intn(6-p), 1 + rng.Intn(6)})
		} else {
			ops = append(ops, op{})
		}
	}
	trace := func() string {
		s, err := NewScheduler(2, 2, 2, 6)
		if err != nil {
			t.Fatal(err)
		}
		out := ""
		for _, o := range ops {
			if o.add {
				out += fmt.Sprintf("a%v;", s.Add(o.id, o.p, o.th, o.w) != nil)
			} else {
				run, done := s.Step()
				out += fmt.Sprintf("s%d,%d;", run, done)
			}
		}
		return out
	}
	if a, b := trace(), trace(); a != b {
		t.Fatalf("replay diverged:\n%s\n%s", a, b)
	}
}
