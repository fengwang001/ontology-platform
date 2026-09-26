// Package api is the public entry point of the in-process leaky bucket
// rate limiter: standard library only, state in process memory, Submit
// takes explicit monotonic integer timestamps.
package api

import (
	"fmt"
	"math/rand"

	"ontology/sched"
)

// Distinct sentinel errors, re-exported so callers need only errors.Is.
var (
	ErrInvalidConfig = sched.ErrInvalidConfig // capacity <= 0 or interval <= 0
	ErrNegativeTime  = sched.ErrNegativeTime  // t < 0
	ErrClockRewind   = sched.ErrClockRewind   // t < a previously seen t
)

// Leaky is a queue-shaped leaky bucket limiter.
type Leaky struct{ s *sched.Submitter }

// New creates a limiter: capacity >= 1 requests may be in the system at
// once, and one leaks out every interval ticks.
func New(capacity, interval int64) (*Leaky, error) {
	s, err := sched.New(capacity, interval)
	if err != nil {
		return nil, err
	}
	return &Leaky{s: s}, nil
}

func (l *Leaky) Submit(t int64) (int64, bool, error) { return l.s.Submit(t) }
func (l *Leaky) InSystem() int                       { return l.s.InSystem() }

// naiveBucket is the deliberately simple reference: an explicit departure
// list popped from the front while the head is <= t.
type naiveBucket struct {
	cap, iv int64
	deps    []int64
}

func (n *naiveBucket) submit(t int64) (int64, bool) {
	for len(n.deps) > 0 && n.deps[0] <= t {
		n.deps = n.deps[1:]
	}
	if int64(len(n.deps)) == n.cap { // full: drop
		return 0, false
	}
	d := t + n.iv
	if len(n.deps) > 0 {
		d = n.deps[len(n.deps)-1] + n.iv // queue behind the previous item
	}
	n.deps = append(n.deps, d)
	return d, true
}

// SelfCheck replays built-in sequences and verifies naive-reference
// agreement, smoothness (gaps >= interval), the capacity bound, and that
// every rejection leaves no trace; it also runs the drain-complexity check.
func (l *Leaky) SelfCheck() error {
	if err := sched.SelfCheck(); err != nil {
		return err
	}
	for name, seq := range builtinSequences() {
		got, _ := New(seq.cap, seq.iv)
		ref := &naiveBucket{cap: seq.cap, iv: seq.iv}
		var prev int64
		have := false
		for i, t := range seq.ts {
			dep, adm, serr := got.Submit(t)
			rdep, radm := ref.submit(t)
			if serr != nil || dep != rdep || adm != radm {
				return fmt.Errorf("seq %d step %d t=%d: got (%d,%v,%v) ref (%d,%v)",
					name, i, t, dep, adm, serr, rdep, radm)
			}
			if got.InSystem() != len(ref.deps) || int64(got.InSystem()) > seq.cap {
				return fmt.Errorf("seq %d step %d: in-system mismatch/overflow", name, i)
			}
			if adm && have && dep < prev+seq.iv {
				return fmt.Errorf("seq %d step %d: deps %d->%d closer than %d",
					name, i, prev, dep, seq.iv)
			}
			if adm {
				prev, have = dep, true
			}
		}
	}
	return rejectionLeavesNoTrace()
}

type sequence struct {
	cap, iv int64
	ts      []int64
}

func builtinSequences() []sequence {
	seqs := []sequence{{3, 5, []int64{0, 5, 6, 10, 15, 15, 15, 21}}} // canonical eight steps
	r := rand.New(rand.NewSource(1))
	for range 8 { // random non-decreasing sequences; ties and bursts included
		iv := int64(1 + r.Intn(5))
		ts := make([]int64, 200)
		t := int64(0)
		for i := range ts {
			t += int64(r.Intn(int(iv) + 2))
			ts[i] = t
		}
		seqs = append(seqs, sequence{int64(1 + r.Intn(6)), iv, ts})
	}
	return seqs
}

// rejectionLeavesNoTrace checks each distinct rejection kind changes no
// state and the limiter keeps working afterwards.
func rejectionLeavesNoTrace() error {
	for _, c := range [][2]int64{{0, 5}, {-1, 5}, {3, 0}, {3, -2}} {
		if _, err := New(c[0], c[1]); err != ErrInvalidConfig {
			return fmt.Errorf("config (%d,%d): %v", c[0], c[1], err)
		}
	}
	l, _ := New(3, 5)
	steps := []struct {
		t   int64
		err error // nil means the step must be admitted
	}{
		{-1, ErrNegativeTime},
		{0, nil},
		{-2, ErrNegativeTime}, // queue dep=5 must survive
		{10, nil},             // drains dep=5, admits dep=15, lastT=10
		{9, ErrClockRewind},   // dep=15 must survive
	}
	for i, st := range steps {
		before := l.InSystem()
		dep, adm, serr := l.Submit(st.t)
		if serr != st.err {
			return fmt.Errorf("step %d: err=%v want %v", i, serr, st.err)
		}
		if st.err != nil && l.InSystem() != before {
			return fmt.Errorf("step %d changed in-system %d->%d", i, before, l.InSystem())
		}
		if st.err == nil && (!adm || dep <= 0) {
			return fmt.Errorf("step %d expected admission, got (%d,%v)", i, dep, adm)
		}
	}
	if dep, adm, serr := l.Submit(10); serr != nil || !adm || dep != 20 {
		return fmt.Errorf("use after rejection: (%d,%v,%v) want 20", dep, adm, serr)
	}
	return nil
}
