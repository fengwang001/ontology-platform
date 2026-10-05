package let

import (
	"math/rand"
	"reflect"
	"testing"
)

// This file cross-checks the closed-form analysis against a naive
// step-by-step simulation. The simulation unrolls every job over several
// hyperperiods, propagates sample tags in time order (each job reads the
// latest value its predecessor wrote at or before the job's release, with
// same-instant visibility), and then evaluates every x in the relevant
// interval:
//
//   - Reaction(x): earliest write of taun carrying a tag >= x, minus x.
//   - Age(x):      x minus the tag of taun's last write at or before x.
//
// A tag is the release time of the tau1 job that took the sample.

// writeEvent is one output write in the simulation.
type writeEvent struct {
	time int // write time r+w
	tag  int // release time of the tau1 job that produced the value
}

// simulate unrolls every job released up to tEnd and returns the write
// events of the last task in the chain, ordered by write time.
func simulate(ts []Task, tEnd int) []writeEvent {
	var prev []writeEvent
	for k, task := range ts {
		var writes []writeEvent
		idx := 0
		tag := -1
		for r := task.Phi; r <= tEnd; r += task.T {
			cur := r // tau1 samples its own input at release
			if k > 0 {
				for idx < len(prev) && prev[idx].time <= r {
					tag = prev[idx].tag
					idx++
				}
				if tag < 0 {
					continue // no input written yet
				}
				cur = tag
			}
			writes = append(writes, writeEvent{time: r + task.W, tag: cur})
		}
		prev = writes
	}
	return prev
}

const inf = int(^uint(0) >> 1)

// simMetrics computes MaxReaction, MinReaction and MaxAge from the
// simulated write events of taun.
func simMetrics(t *testing.T, ts []Task) (maxR, minR, maxAge int) {
	t.Helper()
	phi := maxPhase(ts)
	h := hyperperiod(ts)
	sumT := sumPeriods(ts)
	sumW := 0
	for _, task := range ts {
		sumW += task.W
	}
	// Unroll far enough that every query below is answered by real jobs:
	// reaction queries need tags up to phi+H, age queries need writes up
	// to W+H with W = phi+2*sumT.
	tEnd := phi + h + 2*(sumT+sumW) + 8
	writes := simulate(ts, tEnd)

	// Reaction(x): earliest write with tag >= x. Build suffix minima of
	// write times over tags so every x is answered in O(1).
	firstByTag := make(map[int]int)
	for _, w := range writes {
		if _, ok := firstByTag[w.tag]; !ok {
			firstByTag[w.tag] = w.time
		}
	}
	earliest := make([]int, phi+h+1)
	best := inf
	// Tags may exceed phi+H-1 (a sample taken near the end of the sweep
	// interval can be overwritten by a later sample), so seed the suffix
	// minimum with every write whose tag lies beyond the loop range.
	for _, w := range writes {
		if w.tag >= phi+h-1 && w.time < best {
			best = w.time
		}
	}
	for x := phi + h - 1; x >= 0; x-- {
		if tm, ok := firstByTag[x]; ok && tm < best {
			best = tm
		}
		earliest[x] = best
	}
	minR = inf
	for x := phi; x < phi+h; x++ {
		if earliest[x] == inf {
			t.Fatalf("sim: no write with tag >= %d (tEnd=%d too small)", x, tEnd)
		}
		r := earliest[x] - x
		if r > maxR {
			maxR = r
		}
		if r < minR {
			minR = r
		}
	}

	// Age(x): x minus the tag of the last write at or before x.
	w0 := phi + 2*sumT
	idx := 0
	tag := -1
	for x := w0; x < w0+h; x++ {
		for idx < len(writes) && writes[idx].time <= x {
			tag = writes[idx].tag
			idx++
		}
		if tag < 0 {
			t.Fatalf("sim: no write at or before %d (tEnd=%d too small)", x, tEnd)
		}
		if a := x - tag; a > maxAge {
			maxAge = a
		}
	}
	return maxR, minR, maxAge
}

// sumDelays returns the sum of the chain's write delays.
func sumDelays(ts []Task) int {
	s := 0
	for _, task := range ts {
		s += task.W
	}
	return s
}

// randomChain generates a random valid chain with periods in [1, 12].
func randomChain(rng *rand.Rand) []Task {
	n := 2 + rng.Intn(5) // 2..6 tasks
	ts := make([]Task, n)
	for k := range ts {
		period := 1 + rng.Intn(12)
		ts[k] = Task{
			ID:  string(rune('A' + k)),
			T:   period,
			Phi: rng.Intn(period),
			W:   1 + rng.Intn(period),
		}
	}
	return ts
}

// TestRandomCrossCheck replays 2000 random chains through both the
// closed-form analysis (via the public Analyzer API) and the naive
// simulation, and checks the documented invariants. Every case logs its
// input, both outputs and the verdict (visible with `go test -v`).
func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	const cases = 2000
	for i := 0; i < cases; i++ {
		ts := randomChain(rng)
		if hyperperiod(ts) > MaxHyperperiod {
			i--
			continue
		}
		a := NewAnalyzer()
		ids := make([]string, len(ts))
		for k, task := range ts {
			if err := a.AddTask(task); err != nil {
				t.Fatalf("case %d: AddTask: %v", i, err)
			}
			ids[k] = task.ID
		}
		if err := a.AddChain("c", ids); err != nil {
			t.Fatalf("case %d: AddChain: %v", i, err)
		}
		got, err := a.Analyze("c")
		if err != nil {
			t.Fatalf("case %d: Analyze: %v", i, err)
		}
		maxR, minR, maxAge := simMetrics(t, ts)
		sumW := sumDelays(ts)
		// Verdict basis: closed form must equal the simulation on all
		// three metrics, MaxAge must equal MaxReaction, and MinReaction
		// must lie in [sum(w), MaxReaction].
		ok := got.MaxReaction == maxR &&
			got.MinReaction == minR &&
			got.MaxAge == maxAge &&
			got.MaxAge == got.MaxReaction &&
			minR >= sumW && minR <= maxR
		t.Logf("case %d: in=%v closed-form=%+v sim={MaxReaction:%d MinReaction:%d MaxAge:%d} sumW=%d ok=%v",
			i, ts, got, maxR, minR, maxAge, sumW, ok)
		if !ok {
			t.Errorf("case %d: in=%v closed-form=%+v sim={MaxReaction:%d MinReaction:%d MaxAge:%d} sumW=%d",
				i, ts, got, maxR, minR, maxAge, sumW)
			continue
		}
		// Per-x invariants: every Reaction(x) and every Age(x) is at
		// least the sum of the write delays.
		phi := maxPhase(ts)
		h := hyperperiod(ts)
		for x := phi; x < phi+h; x++ {
			if r := reactionAt(ts, x); r < sumW {
				t.Errorf("case %d: Reaction(%d)=%d < sumW=%d", i, x, r, sumW)
			}
		}
		w0 := phi + 2*sumPeriods(ts)
		for x := w0; x < w0+h; x++ {
			if age := ageAt(ts, x); age < sumW {
				t.Errorf("case %d: Age(%d)=%d < sumW=%d", i, x, age, sumW)
			}
		}
	}
}

// TestRandomTune checks Tune against an independent brute-force search
// over all phase vectors on random chains.
func TestRandomTune(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const cases = 300
	for i := 0; i < cases; i++ {
		n := 2 + rng.Intn(3) // 2..4 tasks, keeps the product small
		ts := make([]Task, n)
		for k := range ts {
			period := 1 + rng.Intn(8)
			ts[k] = Task{
				ID:  string(rune('A' + k)),
				T:   period,
				Phi: rng.Intn(period),
				W:   1 + rng.Intn(period),
			}
		}
		product := 1
		for _, task := range ts[1:] {
			product *= task.T
		}
		if hyperperiod(ts) > MaxHyperperiod || product > MaxTuneProduct {
			i--
			continue
		}
		a := NewAnalyzer()
		ids := make([]string, n)
		for k, task := range ts {
			if err := a.AddTask(task); err != nil {
				t.Fatalf("case %d: AddTask: %v", i, err)
			}
			ids[k] = task.ID
		}
		if err := a.AddChain("c", ids); err != nil {
			t.Fatalf("case %d: AddChain: %v", i, err)
		}
		tr, err := a.Tune("c")
		if err != nil {
			t.Fatalf("case %d: Tune: %v", i, err)
		}

		// Independent brute force: lexicographic sweep, first best wins.
		cur := analyze(ts).MaxReaction
		best := inf
		var bestVec []int
		vec := make([]int, n)
		for {
			trial := make([]Task, n)
			copy(trial, ts)
			for k := 1; k < n; k++ {
				trial[k].Phi = vec[k]
			}
			if m := analyze(trial).MaxReaction; m < best {
				best = m
				bestVec = append([]int(nil), vec[1:]...)
			}
			k := n - 1
			for k >= 1 {
				vec[k]++
				if vec[k] < ts[k].T {
					break
				}
				vec[k] = 0
				k--
			}
			if k < 1 {
				break
			}
		}

		ok := true
		if best < cur {
			ok = tr.Changed &&
				tr.MaxReactionBefore == cur &&
				tr.MaxReactionAfter == best &&
				reflect.DeepEqual(tr.PhasesAfter[1:], bestVec)
			if res, err := a.Analyze("c"); err != nil || res.MaxReaction != best {
				ok = false
			}
		} else {
			ok = !tr.Changed &&
				tr.MaxReactionBefore == cur &&
				tr.MaxReactionAfter == cur &&
				reflect.DeepEqual(tr.PhasesAfter, tr.PhasesBefore)
		}
		t.Logf("case %d: in=%v cur=%d best=%d bestVec=%v tune=%+v ok=%v",
			i, ts, cur, best, bestVec, tr, ok)
		if !ok {
			t.Errorf("case %d: in=%v cur=%d best=%d bestVec=%v tune=%+v",
				i, ts, cur, best, bestVec, tr)
		}
	}
}
