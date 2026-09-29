package scheduler

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// recordedJob tracks everything the simulator needs to validate invariants
// independently of the scheduler's own accounting.
type recordedJob struct {
	nodes    int
	duration Tick
	submitAt Tick
	startAt  Tick
	endAt    Tick
	started  bool
	finished bool
}

// runRandomSimulation drives one scheduler with a random but fully logged
// workload and independently verifies:
//   - no overcommit: running node sum <= N after every operation;
//   - reservation bound: each job starts no later than the shadow time that
//     was computed when it first became the queue head (validated via the
//     simulator's own reservation calculation);
//   - jobs never run past their estimated duration;
//   - every submitted job eventually starts and finishes.
func runRandomSimulation(t *testing.T, rng *rand.Rand, n, ticks, numJobs int) {
	t.Helper()
	var logBuf logBuffer
	logger := newBufferLogger(&logBuf)
	s, err := New(n, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	jobs := make(map[string]*recordedJob)
	var submitTimes []string

	// Bounds recorded by the simulator whenever a job first becomes head:
	// earliest feasible start computed independently from the state then.
	headBounds := make(map[string]Tick)

	submitOne := func(seq int) string {
		id := fmt.Sprintf("J%04d", seq)
		nodes := 1 + rng.Intn(n)
		dur := Tick(1 + rng.Intn(8))
		snap := s.Query()
		// Becoming head: queue currently empty means this job will be head.
		becomesHead := len(snap.QueuedIDs) == 0

		starts, err := s.Submit(Job{ID: id, Nodes: nodes, Duration: dur})
		if err != nil {
			t.Fatalf("Submit %s: %v (seed trace in log)", id, err)
		}
		jobs[id] = &recordedJob{nodes: nodes, duration: dur, submitAt: snap.Time}
		submitTimes = append(submitTimes, id)
		if becomesHead && len(starts) == 0 {
			headBounds[id] = independentShadow(s.Query(), s, id)
		}
		for _, ev := range starts {
			markStart(t, jobs, ev.JobID, ev.Time)
		}
		assertNoOvercommit(t, s, n)
		return id
	}

	seq := 0
	for tt := Tick(1); tt <= Tick(ticks); tt++ {
		// Submit 0-3 jobs at each time.
		for k := rng.Intn(4); k > 0; k-- {
			if seq >= numJobs {
				break
			}
			seq++
			submitOne(seq)
		}

		preSnap := s.Query()
		finishes, starts, err := s.Advance(tt)
		if err != nil {
			t.Fatalf("Advance(%d): %v", tt, err)
		}
		for _, f := range finishes {
			rj := jobs[f.JobID]
			if !rj.started || rj.finished {
				t.Fatalf("bad finish event %+v for job state %+v", f, rj)
			}
			rj.finished = true
			if f.Forced && f.Time != rj.endAt {
				t.Fatalf("job %s force-finished at %d, estimated end %d",
					f.JobID, f.Time, rj.endAt)
			}
		}
		// A new head may appear at the new time; if it did not start now,
		// record its independently computed shadow bound.
		postSnap := s.Query()
		for _, ev := range starts {
			markStart(t, jobs, ev.JobID, ev.Time)
		}
		if len(postSnap.QueuedIDs) > 0 {
			head := postSnap.QueuedIDs[0]
			if _, ok := headBounds[head]; !ok {
				bound := independentShadow(postSnap, s, head)
				if bound > postSnap.Time {
					headBounds[head] = bound
				}
			}
		}
		_ = preSnap
		assertNoOvercommit(t, s, n)

		// Occasionally finish a random running job early.
		snap := s.Query()
		if len(snap.RunningIDs) > 0 && rng.Intn(3) == 0 {
			victim := snap.RunningIDs[rng.Intn(len(snap.RunningIDs))]
			fins, st, ferr := s.Finish(victim)
			if ferr != nil {
				t.Fatalf("Finish(%s): %v", victim, ferr)
			}
			jobs[victim].finished = true
			if len(fins) != 1 || fins[0].Forced {
				t.Fatalf("early finish %s returned %v", victim, fins)
			}
			for _, ev := range st {
				markStart(t, jobs, ev.JobID, ev.Time)
			}
			assertNoOvercommit(t, s, n)
		}
	}

	// Drain: advance far enough to force everything to finish.
	for {
		snap := s.Query()
		if len(snap.QueuedIDs) == 0 && len(snap.RunningIDs) == 0 {
			break
		}
		_, starts, err := s.Advance(snap.Time + 1)
		if err != nil {
			t.Fatalf("drain Advance: %v", err)
		}
		for _, ev := range starts {
			markStart(t, jobs, ev.JobID, ev.Time)
		}
		// Mark forced finishes.
		snap2 := s.Query()
		for id, rj := range jobs {
			if rj.started && !rj.finished && !contains(snap2.RunningIDs, id) &&
				!contains(snap2.QueuedIDs, id) {
				if snap2.Time < rj.endAt {
					t.Fatalf("job %s vanished at %d before its end %d", id, snap2.Time, rj.endAt)
				}
				rj.finished = true
			}
		}
		assertNoOvercommit(t, s, n)
		if snap.Time > Tick(ticks)+1000 {
			t.Fatalf("drain did not converge")
		}
	}

	// Final assertions: reservation upper bound and completion.
	for id, rj := range jobs {
		if !rj.started {
			t.Fatalf("job %s never started", id)
		}
		if !rj.finished {
			t.Fatalf("job %s never finished", id)
		}
		if rj.endAt-rj.startAt != rj.duration {
			t.Fatalf("job %s runtime window %d != duration %d",
				id, rj.endAt-rj.startAt, rj.duration)
		}
		if bound, ok := headBounds[id]; ok && rj.startAt > bound {
			t.Fatalf("RESERVATION BOUND VIOLATED: job %s started at %d > shadow %d",
				id, rj.startAt, bound)
		}
	}

	// Determinism: replay the exact same Submit/Advance/Finish sequence is
	// covered separately against the start sequence; here we at least make
	// sure the log captured inputs, outputs and decisions.
	text := logBuf.String()
	for _, marker := range []string{"input Submit", "output Submit", "input Advance",
		"output Advance", "decision reservation", "decision backfill"} {
		if !containsString(text, marker) {
			// Not every run hits every marker; only require decision/start
			// logging presence when reservations happened.
			if marker == "decision reservation" && len(headBounds) == 0 {
				continue
			}
			if marker == "decision backfill" {
				continue
			}
			t.Fatalf("simulation log missing %q", marker)
		}
	}
	_ = submitTimes
}

func markStart(t *testing.T, jobs map[string]*recordedJob, id string, at Tick) {
	t.Helper()
	rj, ok := jobs[id]
	if !ok {
		t.Fatalf("start for unknown job %s", id)
	}
	if rj.started {
		t.Fatalf("job %s started twice", id)
	}
	rj.started = true
	rj.startAt = at
	rj.endAt = at + rj.duration
}

func assertNoOvercommit(t *testing.T, s *Scheduler, n int) {
	t.Helper()
	snap := s.Query()
	if snap.UsedNodes > n {
		t.Fatalf("OVERCOMMIT: used=%d > N=%d at t=%d", snap.UsedNodes, n, snap.Time)
	}
}

// independentShadow recomputes the queue head's earliest feasible start from
// the scheduler's observable state, mimicking the spec without trusting the
// scheduler's internal reservation.
func independentShadow(snap Snapshot, s *Scheduler, headID string) Tick {
	type rjInfo struct {
		id    string
		nodes int
		end   Tick
	}
	var running []rjInfo
	used := 0
	for _, id := range snap.RunningIDs {
		info, ok := s.RunningInfo(id)
		if !ok {
			continue
		}
		running = append(running, rjInfo{id: id, nodes: info.Nodes, end: info.End})
		used += info.Nodes
	}
	headNodes := 0
	for _, id := range snap.QueuedIDs {
		if id == headID {
			info, ok := s.QueuedJob(id)
			if !ok {
				continue
			}
			headNodes = info.Nodes
		}
	}
	// Canonical release order: end asc, id asc.
	for i := 1; i < len(running); i++ {
		for j := i; j > 0; j-- {
			if running[j].end < running[j-1].end ||
				(running[j].end == running[j-1].end && running[j].id < running[j-1].id) {
				running[j], running[j-1] = running[j-1], running[j]
			}
		}
	}
	released := 0
	var shadow Tick = snap.Time
	for _, rj := range running {
		if used-released+headNodes <= s.nSafe() {
			break
		}
		released += rj.nodes
		shadow = rj.end
	}
	return shadow
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && stringContains(haystack, needle)
}

func TestRandomLoadReservationBound(t *testing.T) {
	configs := []struct {
		seed              int64
		n, ticks, numJobs int
	}{
		{1, 4, 60, 40},
		{2, 8, 80, 80},
		{3, 1, 40, 20},
		{4, 16, 120, 200},
		{5, 3, 100, 60},
		{6, 5, 70, 70},
		// Regression: seed 1016 exposed a double-start where backfilled jobs
		// were scanned from a stale snapshot of the queue.
		{1016, 1 + int(1016%20), 60, 120},
	}
	for _, cfg := range configs {
		cfg := cfg
		t.Run(fmt.Sprintf("seed=%d_n=%d", cfg.seed, cfg.n), func(t *testing.T) {
			rng := rand.New(rand.NewSource(cfg.seed))
			runRandomSimulation(t, rng, cfg.n, cfg.ticks, cfg.numJobs)
		})
	}
}

// TestConcurrentStress hammers Submit/Finish/Advance/Query concurrently to
// exercise the locking under -race; correctness of outcomes is asserted via
// Query snapshots.
func TestConcurrentStress(t *testing.T) {
	s, _ := newTestScheduler(t, 6)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() { // clock
		defer wg.Done()
		for tt := Tick(1); ; tt++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, _, err := s.Advance(tt); err != nil {
				return
			}
		}
	}()

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w + 100)))
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("w%d-%d", w, i)
				_, _ = s.Submit(Job{ID: id, Nodes: 1 + rng.Intn(6), Duration: Tick(1 + rng.Intn(5))})
			}
		}(w)
	}

	wg.Add(1)
	go func() { // queries + occasional finishes
		defer wg.Done()
		rng := rand.New(rand.NewSource(7))
		for i := 0; i < 400; i++ {
			snap := s.Query()
			if snap.UsedNodes > 6 {
				t.Errorf("overcommit under concurrency: %d", snap.UsedNodes)
				return
			}
			if len(snap.RunningIDs) > 0 && rng.Intn(4) == 0 {
				_, _, _ = s.Finish(snap.RunningIDs[rng.Intn(len(snap.RunningIDs))])
			}
		}
		close(stop)
	}()

	wg.Wait()
}
